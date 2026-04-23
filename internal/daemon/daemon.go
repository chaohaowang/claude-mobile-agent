package daemon

import (
	"context"
	stdBase64 "encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/chaohaow/claude-mobile-agent/internal/asr"
	"github.com/chaohaow/claude-mobile-agent/internal/config"
	"github.com/chaohaow/claude-mobile-agent/internal/jsonltail"
	"github.com/chaohaow/claude-mobile-agent/internal/relay"
	"github.com/chaohaow/claude-mobile-agent/internal/sessionmgr"
	"github.com/chaohaow/claude-mobile-agent/internal/tmuxctl"
	"github.com/chaohaow/claude-mobile-agent/internal/wire"
)

type Daemon struct {
	cfg       *config.Config
	tmux      *tmuxctl.Client
	relay     *relay.Client
	asr       *asr.Client // nil if no API key was set in env
	seq       atomic.Uint64
	sessID    string
	pathMu    sync.Mutex
	jsonlPath string // guarded by pathMu; used by history-req handler
}

func (d *Daemon) getJSONLPath() string {
	d.pathMu.Lock()
	defer d.pathMu.Unlock()
	return d.jsonlPath
}

func (d *Daemon) setJSONLPath(p string) {
	d.pathMu.Lock()
	d.jsonlPath = p
	d.pathMu.Unlock()
}

func New(cfg *config.Config) *Daemon {
	d := &Daemon{
		cfg:    cfg,
		tmux:   tmuxctl.New(),
		relay:  relay.New(cfg.Relay.URL, cfg.Relay.PairID, "agent", cfg.Relay.DeviceID),
		sessID: cfg.Session.Name,
	}
	if c, err := asr.New(); err == nil {
		d.asr = c
	} else {
		log.Printf("ASR disabled: %v", err)
	}
	return d
}

func (d *Daemon) Run(ctx context.Context) error {
	// Auto-detect: if tmux_target is empty, scan tmux for a pane already running
	// claude in our cwd and adopt it. Lets users attach the daemon to their
	// existing session without hand-editing the config.
	if d.cfg.Session.TmuxTarget == "" {
		if target, ok := d.tmux.FindClaudePaneAt(d.cfg.Session.CWD); ok {
			log.Printf("auto-detected tmux target at cwd=%s: %s", d.cfg.Session.CWD, target)
			d.cfg.Session.TmuxTarget = target
		}
	}

	// View-only mode: when tmux_target is (still) empty, skip the tmux check
	// and the inbound handler. Outbound (jsonl → phone) still works;
	// phone-sent frames are dropped with a log message.
	viewOnly := d.cfg.Session.TmuxTarget == ""

	// 1. sanity: tmux session must exist (only if not view-only)
	if !viewOnly {
		ok, err := d.tmux.HasSession(d.cfg.Session.TmuxTarget)
		if err != nil {
			return fmt.Errorf("tmux check: %w", err)
		}
		if !ok {
			return fmt.Errorf("tmux session %q not found — start it first", d.cfg.Session.TmuxTarget)
		}
	} else {
		log.Printf("view-only mode: no tmux session found at cwd=%s; phone messages will be ignored", d.cfg.Session.CWD)
	}

	// 2. locate jsonl
	home, _ := os.UserHomeDir()
	projectsRoot := filepath.Join(home, ".claude", "projects")
	jsonlPath, err := sessionmgr.FindActiveJSONL(projectsRoot, d.cfg.Session.CWD)
	if err != nil {
		return fmt.Errorf("locate jsonl: %w", err)
	}
	d.setJSONLPath(jsonlPath)
	log.Printf("tailing jsonl: %s", jsonlPath)

	// 3. connect relay
	if err := d.relay.Connect(ctx); err != nil {
		return fmt.Errorf("relay connect: %w", err)
	}

	// 4. start tail goroutine with rotation watcher — Claude Code opens a new
	// jsonl on /clear or session rotation, so we re-check periodically and
	// swap the tailer when a newer file appears.
	records := make(chan jsonltail.Record, 64)
	go d.tailWithRotation(ctx, projectsRoot, records)

	// 5. start inbound-frame handler → tmux send-keys (skip in view-only)
	if !viewOnly {
		go d.handleIncoming(ctx)
		go d.watchStatus(ctx) // poll tmux pane for Claude Code's activity line
	} else {
		go d.drainIncoming(ctx)
	}

	// 6. forward records to relay
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case rec, ok := <-records:
			if !ok {
				return nil
			}
			frame := wire.Frame{
				Type: wire.FrameTypeSessionMessage,
				Seq:  d.seq.Add(1),
				Payload: wire.SessionMessage{
					SessionID: d.sessID,
					Msg: wire.Message{
						Role:    rec.Role,
						Content: rec.Content,
						TS:      rec.TS,
						ID:      rec.UUID,
					},
				},
			}
			if err := d.relay.Send(frame); err != nil {
				log.Printf("send failed (will drop, reconnect loop takes over): %v", err)
			}
		}
	}
}

// tailWithRotation runs a tailer for the current jsonl and periodically checks
// whether a newer jsonl has appeared in the project directory (which happens
// when Claude Code starts a new session, e.g. after /clear). When rotation is
// detected, it cancels the current tailer and starts a new one tailing the
// new file from the beginning — a fresh jsonl is small and we want the phone
// to see whatever has already been written there.
func (d *Daemon) tailWithRotation(ctx context.Context, projectsRoot string, out chan<- jsonltail.Record) {
	var (
		tailCancel context.CancelFunc
		startFromBeginning bool // first tailer keeps default (tail-from-end)
	)
	spawn := func(path string) {
		tailCtx, cancel := context.WithCancel(ctx)
		tailCancel = cancel
		t := jsonltail.NewTailer(path, 200*time.Millisecond)
		t.StartFromBeginning = startFromBeginning
		go func() { _ = t.Run(tailCtx, out) }()
	}
	spawn(d.getJSONLPath())
	startFromBeginning = true // subsequent rotations: read the new file fully

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if tailCancel != nil {
				tailCancel()
			}
			return
		case <-ticker.C:
			newest, err := sessionmgr.FindActiveJSONL(projectsRoot, d.cfg.Session.CWD)
			if err != nil {
				continue
			}
			if newest != d.getJSONLPath() {
				log.Printf("jsonl rotated: %s → %s", d.getJSONLPath(), newest)
				tailCancel()
				d.setJSONLPath(newest)
				spawn(newest)
			}
		}
	}
}

func (d *Daemon) handleIncoming(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-d.relay.Incoming():
			if !ok {
				return
			}
			switch p := f.Payload.(type) {
			case wire.SessionSend:
				if p.SessionID != d.sessID {
					continue
				}
				if err := d.tmux.SendLine(d.cfg.Session.TmuxTarget, p.Text); err != nil {
					log.Printf("send-keys failed: %v", err)
				}
			case wire.SessionInterrupt:
				if p.SessionID != d.sessID {
					continue
				}
				if err := d.tmux.SendCtrlC(d.cfg.Session.TmuxTarget); err != nil {
					log.Printf("send Ctrl-C failed: %v", err)
				}
			case wire.SessionHistoryReq:
				d.replyHistory(p)
			case wire.ASRRequest:
				go d.handleASR(p)
			}
		}
	}
}

// replyHistory reads the last N records from the jsonl and sends them back
// to the relay as session.message frames. Called when a phone connects and
// asks for recent context.
func (d *Daemon) replyHistory(req wire.SessionHistoryReq) {
	if req.SessionID != d.sessID {
		return
	}
	n := req.Last
	if n <= 0 {
		n = 50
	}
	if n > 200 {
		n = 200 // cap to keep the relay buffer happy
	}
	recs, err := jsonltail.LastN(d.getJSONLPath(), n)
	if err != nil {
		log.Printf("history read failed: %v", err)
		return
	}
	log.Printf("replying with %d history records", len(recs))
	for _, rec := range recs {
		frame := wire.Frame{
			Type: wire.FrameTypeSessionMessage,
			Seq:  d.seq.Add(1),
			Payload: wire.SessionMessage{
				SessionID: d.sessID,
				Msg: wire.Message{
					Role:    rec.Role,
					Content: rec.Content,
					TS:      rec.TS,
					ID:      rec.UUID,
				},
			},
		}
		if err := d.relay.Send(frame); err != nil {
			log.Printf("history send failed: %v", err)
			return
		}
		// pace the burst so the relay's fanout outbox (64 slots) doesn't drop
		time.Sleep(10 * time.Millisecond)
	}
}

// watchStatus polls the tmux pane every 500ms and pushes Claude Code's
// activity line (e.g. "✳ Smooshing… (31s · thinking more…)") as a
// session.status frame whenever it changes. Empty string means idle.
func (d *Daemon) watchStatus(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pane, err := d.tmux.CapturePane(d.cfg.Session.TmuxTarget)
		if err != nil {
			continue
		}
		status := extractStatusLine(pane)
		preview := ""
		if status != "" {
			preview = extractPreview(pane)
		}
		meta := extractMeta(pane)
		combined := status + "\x00" + preview + "\x00" + meta
		if combined == last {
			continue
		}
		last = combined
		frame := wire.Frame{
			Type: wire.FrameTypeSessionStatus,
			Seq:  d.seq.Add(1),
			Payload: wire.SessionStatus{
				SessionID: d.sessID,
				Status:    status,
				Preview:   preview,
				Meta:      meta,
			},
		}
		_ = d.relay.Send(frame)
	}
}

// extractPreview returns ONLY the currently-streaming assistant text block
// (the last "⏺ …" paragraph), skipping tool invocations (⏺ Bash(...))
// and tool results (⎿ …). Returns "" if the last block is a tool call or
// there's nothing to show.
//
// Claude Code's TUI renders:
//
//	⏺ assistant text line               (marker, 0 indent)
//	  continuation of text              (2-space indent)
//	⏺ Bash(cmd)                         (marker + tool_use)
//	      args continuation             (6+ space indent)
//	  ⎿  tool result                    (⎿ prefix, 2-space indent)
//
// So for the preview we walk up from the spinner to the last `⏺` line,
// skip it if it's a tool_use, then collect lines until we hit another marker
// (⏺, ⎿) or a line indented > 3 spaces (tool args) — everything else is
// assistant prose and makes it into the preview.
func extractPreview(pane string) string {
	lines := strings.Split(pane, "\n")
	end := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		if looksLikeSpinner(l) {
			end = i
			break
		}
	}
	startIdx := -1
	for i := end - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "⏺") {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return ""
	}
	first := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[startIdx]), "⏺"))
	if looksLikeToolInvocation(first) {
		return ""
	}

	var out []string
	for i := startIdx; i < end; i++ {
		l := lines[i]
		tl := strings.TrimSpace(l)
		if i > startIdx {
			if strings.HasPrefix(tl, "⏺") || strings.HasPrefix(tl, "⎿") {
				break
			}
			// Lines indented more than 3 spaces are tool_use args / code.
			if leadingSpaces(l) > 3 {
				break
			}
		}
		out = append(out, l)
	}
	if len(out) > 0 {
		out[0] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out[0]), "⏺"))
	}
	return strings.TrimRight(strings.Join(out, "\n"), " \t\n")
}

func leadingSpaces(s string) int {
	n := 0
	for _, c := range s {
		if c == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}

// looksLikeSpinner matches Claude Code's activity line format:
// "<glyph> Verb… (Ns · …)".
func looksLikeSpinner(l string) bool {
	return strings.Contains(l, "…") && strings.Contains(l, "(") && strings.Contains(l, ")")
}

// looksLikeToolInvocation matches lines like "Bash(ls)", "Edit(foo.ts)" —
// i.e. a capitalized word followed by parentheses, which is how Claude Code
// renders tool calls above their result lines.
func looksLikeToolInvocation(s string) bool {
	pi := strings.Index(s, "(")
	if pi <= 0 {
		return false
	}
	name := s[:pi]
	if len(name) == 0 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	if r < 'A' || r > 'Z' {
		return false
	}
	for _, c := range name {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}

// extractStatusLine scans the captured pane for Claude Code's activity line
// like "✳ Smooshing… (31s · thinking more…)". Returns just that line — the
// top app bar is not a good place for the follow-up ⎿ tip, so we drop it.
func extractStatusLine(pane string) string {
	lines := strings.Split(pane, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		if looksLikeSpinner(l) {
			return l
		}
	}
	return ""
}

// extractMeta returns Claude Code's TUI footer lines — the ctx/model/cwd
// row and the permission-mode row ("⏵⏵ bypass permissions on …") — joined by
// "\n". Either row may be missing; returns "" when nothing matches. These live
// just below the input box in the terminal UI and are useful context to mirror
// to the phone's chat screen.
func extractMeta(pane string) string {
	lines := strings.Split(pane, "\n")
	var model, perm string
	// Scan bottom-up; stop once both lines are found, or we've exhausted the
	// last 8 non-empty lines (footer never lives further up than that).
	seenNonEmpty := 0
	for i := len(lines) - 1; i >= 0 && seenNonEmpty < 8 && (model == "" || perm == ""); i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		seenNonEmpty++
		switch {
		case perm == "" && strings.Contains(l, "⏵⏵"):
			perm = l
		case model == "" && (strings.Contains(l, "ctx:") ||
			strings.Contains(l, "Opus") ||
			strings.Contains(l, "Sonnet") ||
			strings.Contains(l, "Haiku")):
			model = l
		}
	}
	out := make([]string, 0, 2)
	if model != "" {
		out = append(out, model)
	}
	if perm != "" {
		out = append(out, perm)
	}
	return strings.Join(out, "\n")
}

// handleASR receives a phone-recorded clip, runs it through the Bailian ASR
// proxy, and returns the transcript as an asr.result frame. Any error gets
// reported back via the same frame's Error field so the phone can surface it.
func (d *Daemon) handleASR(req wire.ASRRequest) {
	result := wire.ASRResult{RequestID: req.RequestID}
	defer func() {
		frame := wire.Frame{
			Type:    wire.FrameTypeASRResult,
			Seq:     d.seq.Add(1),
			Payload: result,
		}
		if err := d.relay.Send(frame); err != nil {
			log.Printf("asr: send result failed: %v", err)
		}
	}()

	if d.asr == nil {
		result.Error = "ASR not configured on agent (missing BAILIAN_API_KEY)"
		return
	}
	audio, err := stdBase64.StdEncoding.DecodeString(req.AudioB64)
	if err != nil {
		result.Error = fmt.Sprintf("decode audio: %v", err)
		return
	}
	log.Printf("asr: transcribing %d bytes (format=%s, req=%s)", len(audio), req.Format, req.RequestID)
	transcript, err := d.asr.Transcribe(audio, req.Format)
	if err != nil {
		log.Printf("asr: transcribe failed: %v", err)
		result.Error = err.Error()
		return
	}
	log.Printf("asr: raw → %q", truncateStr(transcript, 80))
	// Fold filler words and fix punctuation via the normalization model. On
	// failure we fall back to the raw transcript — better something than
	// nothing, but log it so we can tune the prompt later.
	cleaned, err := d.asr.Normalize(transcript)
	if err != nil {
		log.Printf("asr: normalize failed, using raw: %v", err)
		cleaned = transcript
	} else if cleaned != transcript {
		log.Printf("asr: cleaned → %q", truncateStr(cleaned, 80))
	}
	result.Transcript = cleaned
}

func truncateStr(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "…"
}

// drainIncoming consumes frames in view-only mode. Handles history requests
// (so restart-the-app "show me context" works) and drops everything else
// with a log message.
func (d *Daemon) drainIncoming(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-d.relay.Incoming():
			if !ok {
				return
			}
			switch p := f.Payload.(type) {
			case wire.SessionHistoryReq:
				d.replyHistory(p)
				continue
			case wire.ASRRequest:
				go d.handleASR(p)
				continue
			}
			log.Printf("view-only: dropping inbound frame type=%s seq=%d", f.Type, f.Seq)
		}
	}
}

