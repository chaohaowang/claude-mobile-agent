// Package daemon owns the long-lived process that mirrors every Claude
// Code session on the Mac to the relay over a single WebSocket. The
// daemon polls tmux, owns one liveSession per discovered cwd, multiplexes
// outbound frames from all sessions, and routes inbound frames to the
// right liveSession by payload.session_id (which IS the cwd path).
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

	"github.com/chaohaowang/claude-mobile-agent/internal/asr"
	"github.com/chaohaowang/claude-mobile-agent/internal/host"
	"github.com/chaohaowang/claude-mobile-agent/internal/jsonltail"
	"github.com/chaohaowang/claude-mobile-agent/internal/relay"
	"github.com/chaohaowang/claude-mobile-agent/internal/sessionmgr"
	"github.com/chaohaowang/claude-mobile-agent/internal/tmuxctl"
	"github.com/chaohaowang/claude-mobile-agent/internal/wire"
)

// sessionWatcher is what each per-session worker exposes. Superset of
// sessionmgr.Watcher — adds inbound frame handlers so the dispatcher
// can route phone-originated frames to the right session.
type sessionWatcher interface {
	SessionID() string
	Stop()
	HandleHistory(req wire.SessionHistoryReq)
	HandleSend(req wire.SessionSend)
	HandleInterrupt(req wire.SessionInterrupt)
	HandleASR(req wire.ASRRequest)
}

// Daemon multiplexes every live Claude session on this Mac onto one
// relay WebSocket. The constructor takes host.Config so the relay
// connection uses the Mac's stable host_id as its pair key — Task 6
// wires this up from cmd/claude-mobile.
type Daemon struct {
	cfg       host.Config
	relay     *relay.Client
	tmux      *tmuxctl.Client
	asrClient *asr.Client
	registry  *sessionmgr.Registry
	outbound  chan wire.Frame
	seq       atomic.Uint64

	// sessions: a test-bypass map. Production path leaves this nil and
	// dispatcher falls through to registry. Tests install fakes here so
	// they don't need a real Registry / spawn path.
	sessionsMu sync.Mutex
	sessions   map[string]sessionWatcher
}

// New builds a daemon ready to Run. The host.Config carries HostID
// (relay pair key) and RelayURL.
func New(cfg host.Config) *Daemon {
	d := &Daemon{
		cfg:      cfg,
		tmux:     tmuxctl.New(),
		registry: sessionmgr.NewRegistry(),
		outbound: make(chan wire.Frame, 256),
	}
	if c, err := asr.New(); err == nil {
		d.asrClient = c
	} else {
		log.Printf("ASR disabled: %v", err)
	}
	return d
}

// Run blocks until ctx is cancelled. It connects to the relay, kicks off
// the tmux scanner, the inbound router, and the outbound multiplexer,
// then waits for shutdown.
func (d *Daemon) Run(ctx context.Context) error {
	d.relay = relay.New(d.cfg.RelayURL, d.cfg.HostID, "agent", "mac-host")
	// Push a fresh session.list on every (re)connect. Otherwise a phone that
	// reloaded during the gap (or our daemon got bounced) would sit empty
	// until a tmux pane change forced a broadcast — looks like a frozen UI.
	d.relay.OnConnect = d.broadcastSessionList
	if err := d.relay.Connect(ctx); err != nil {
		return fmt.Errorf("relay connect: %w", err)
	}

	go d.scanLoop(ctx)
	go d.outboundLoop(ctx)
	go d.inboundLoop(ctx)

	<-ctx.Done()
	d.registry.StopAll()
	return ctx.Err()
}

// scanLoop polls tmux every 2 s, finds every pane running `claude`, and
// reconciles the registry against the discovered cwd set.
func (d *Daemon) scanLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	// Run an immediate scan so the first session shows up without a
	// 2-second cold start.
	d.scanAndSync(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.scanAndSync(ctx)
		}
	}
}

func (d *Daemon) scanAndSync(ctx context.Context) {
	panes, err := tmuxctl.ListAllClaudePanes()
	if err != nil {
		log.Printf("scan: %v", err)
		return
	}
	// Dedupe by cwd: multiple panes in the same cwd → one session.
	// The first-seen pane wins as the send-keys target.
	seen := make(map[string]string, len(panes))
	ids := make([]string, 0, len(panes))
	for _, p := range panes {
		if _, ok := seen[p.CWD]; ok {
			continue
		}
		seen[p.CWD] = p.Target
		ids = append(ids, p.CWD)
	}

	added, removed := d.registry.Sync(ids, func(id string) sessionmgr.Watcher {
		// spawn runs under the registry mutex — keep cheap. liveSession's
		// constructor only schedules goroutines, no blocking I/O.
		return d.spawnLiveSession(ctx, id, seen[id])
	})
	if len(added) > 0 || len(removed) > 0 {
		log.Printf("session set: +%v -%v (now %d)", added, removed, len(ids))
		d.broadcastSessionList()
	}
}

// inboundLoop pumps frames from the relay into dispatch.
func (d *Daemon) inboundLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-d.relay.Incoming():
			if !ok {
				return
			}
			d.dispatch(f)
		}
	}
}

// outboundLoop drains outbound (every liveSession's emit) onto the relay.
// Stamps Seq monotonically so the phone can dedupe. On Send failure (relay
// WS dropped mid-send) we hold the frame and retry every 500 ms instead of
// dropping it — without this, a brief reconnect window silently swallows any
// frame in flight (e.g. session.list, history records, ASR results).
func (d *Daemon) outboundLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-d.outbound:
			f.Seq = d.seq.Add(1)
			loggedFail := false
			for {
				if err := d.relay.Send(f); err == nil {
					break
				} else if !loggedFail {
					log.Printf("relay send: %v (will retry until reconnect)", err)
					loggedFail = true
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(500 * time.Millisecond):
				}
			}
		}
	}
}

// dispatch routes one inbound frame to the right liveSession by
// payload.session_id (the cwd). Unknown sessions are silently dropped —
// the phone might be talking about a session that just exited.
func (d *Daemon) dispatch(f wire.Frame) {
	switch p := f.Payload.(type) {
	case wire.SessionHistoryReq:
		if w := d.getWatcher(p.SessionID); w != nil {
			w.HandleHistory(p)
		}
	case wire.SessionSend:
		if w := d.getWatcher(p.SessionID); w != nil {
			w.HandleSend(p)
		}
	case wire.SessionInterrupt:
		if w := d.getWatcher(p.SessionID); w != nil {
			w.HandleInterrupt(p)
		}
	case wire.SessionListReq:
		d.broadcastSessionList()
	case wire.Ping:
		// Reply so the phone can detect dead WS within its pong-timeout
		// window. Drop on outbound saturation; the next ping will re-probe.
		select {
		case d.outbound <- wire.Frame{Type: wire.FrameTypePong, Payload: wire.Pong{}}:
		default:
		}
	case wire.ASRRequest:
		if w := d.getWatcher(p.SessionID); w != nil {
			w.HandleASR(p)
		}
	}
}

// getWatcher returns the watcher for a session_id, or nil if absent.
// Test-injected sessions take priority; production path consults the
// registry.
func (d *Daemon) getWatcher(id string) sessionWatcher {
	d.sessionsMu.Lock()
	if d.sessions != nil {
		if w, ok := d.sessions[id]; ok {
			d.sessionsMu.Unlock()
			return w
		}
	}
	d.sessionsMu.Unlock()
	if d.registry == nil {
		return nil
	}
	w := d.registry.Get(id)
	if w == nil {
		return nil
	}
	if sw, ok := w.(sessionWatcher); ok {
		return sw
	}
	return nil
}

// broadcastSessionList sends the current session set to the phone. Called
// after every scan that changed the set, and on demand for SessionListReq.
func (d *Daemon) broadcastSessionList() {
	var sessions []wire.SessionInfo
	// Test-injected first.
	d.sessionsMu.Lock()
	if d.sessions != nil {
		for id := range d.sessions {
			sessions = append(sessions, wire.SessionInfo{
				ID:   id,
				Name: filepath.Base(id),
				CWD:  id,
			})
		}
	}
	d.sessionsMu.Unlock()
	// Production registry.
	if d.registry != nil {
		for _, id := range d.registry.IDs() {
			sessions = append(sessions, wire.SessionInfo{
				ID:   id,
				Name: filepath.Base(id),
				CWD:  id,
			})
		}
	}
	// Non-blocking send: if the relay's outbound channel is saturated, drop
	// the session.list update rather than wedge the inbound loop. Phone
	// will catch up on the next scan-driven broadcast or session.list.req.
	frame := wire.Frame{
		Type:    wire.FrameTypeSessionList,
		Payload: wire.SessionList{Sessions: sessions},
	}
	select {
	case d.outbound <- frame:
	default:
		log.Printf("outbound full, dropped session.list (%d sessions)", len(sessions))
	}
}

// ---------------------------------------------------------------------
// liveSession — one per discovered cwd. Owns its jsonl tailer (with
// rotation handling), its tmux status poller, and the inbound handlers
// for that session. All outbound traffic flows through d.outbound.
// ---------------------------------------------------------------------

type liveSession struct {
	d          *Daemon
	cwd        string // also the session_id
	tmuxTarget string // pane target captured at spawn time
	ctx        context.Context
	cancel     context.CancelFunc

	pathMu    sync.Mutex
	jsonlPath string
}

func (s *liveSession) SessionID() string { return s.cwd }
func (s *liveSession) Stop()              { s.cancel() }

func (s *liveSession) currentJSONLPath() string {
	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	return s.jsonlPath
}
func (s *liveSession) setJSONLPath(p string) {
	s.pathMu.Lock()
	s.jsonlPath = p
	s.pathMu.Unlock()
}

// spawnLiveSession constructs a liveSession and kicks off its goroutines.
// Runs under the registry mutex (per Sync's contract) — must NOT block
// or call back into Registry methods. All heavy lifting happens in the
// goroutines started here.
func (d *Daemon) spawnLiveSession(parent context.Context, cwd, tmuxTarget string) sessionWatcher {
	home, _ := os.UserHomeDir()
	projectsRoot := filepath.Join(home, ".claude", "projects")

	// Best-effort: resolve initial jsonl path. May be empty if Claude
	// hasn't written the first record yet — tail loop will retry.
	jsonl, err := sessionmgr.FindActiveJSONL(projectsRoot, cwd)
	if err != nil {
		log.Printf("session %s: locate jsonl: %v (will retry in tail loop)", cwd, err)
	}

	// Derive a per-session ctx from the parent so daemon shutdown
	// cascades. Stop() also cancels it directly.
	ctx, cancel := context.WithCancel(parent)
	s := &liveSession{
		d:          d,
		cwd:        cwd,
		tmuxTarget: tmuxTarget,
		ctx:        ctx,
		cancel:     cancel,
		jsonlPath:  jsonl,
	}
	log.Printf("session +: %s (target=%s, jsonl=%s)", cwd, tmuxTarget, jsonl)

	go s.tailLoop(ctx, projectsRoot)
	go s.statusLoop(ctx)
	return s
}

// tailLoop tails the jsonl with rotation handling — Claude Code starts a
// new file on /clear, and we want the new content to flow without losing
// the live read. Mirrors the old daemon's tailWithRotation, parameterized
// per session.
func (s *liveSession) tailLoop(ctx context.Context, projectsRoot string) {
	records := make(chan jsonltail.Record, 64)

	var (
		tailCancel         context.CancelFunc
		startFromBeginning bool // first tailer keeps default (tail-from-end)
	)
	spawn := func(path string) {
		if path == "" {
			return
		}
		tailCtx, cancel := context.WithCancel(ctx)
		tailCancel = cancel
		t := jsonltail.NewTailer(path, 200*time.Millisecond)
		t.StartFromBeginning = startFromBeginning
		go func() { _ = t.Run(tailCtx, records) }()
	}

	// Forward incoming records to the multiplex outbound channel.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case rec, ok := <-records:
				if !ok {
					return
				}
				select {
				case s.d.outbound <- wire.Frame{
					Type: wire.FrameTypeSessionMessage,
					Payload: wire.SessionMessage{
						SessionID: s.cwd,
						Msg: wire.Message{
							Role:    rec.Role,
							Content: rec.Content,
							TS:      rec.TS,
							ID:      rec.UUID,
						},
					},
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	if p := s.currentJSONLPath(); p != "" {
		spawn(p)
		startFromBeginning = true
	}

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
			newest, err := sessionmgr.FindActiveJSONL(projectsRoot, s.cwd)
			if err != nil {
				continue
			}
			cur := s.currentJSONLPath()
			if newest != cur {
				if cur != "" {
					log.Printf("session %s: jsonl rotated: %s → %s", s.cwd, cur, newest)
				} else {
					log.Printf("session %s: jsonl appeared: %s", s.cwd, newest)
				}
				if tailCancel != nil {
					tailCancel()
				}
				s.setJSONLPath(newest)
				spawn(newest)
				startFromBeginning = true
			}
		}
	}
}

// statusLoop polls the tmux pane every 500 ms and emits session.status
// frames whenever the spinner / preview / footer-meta combo changes.
// Skipped when there's no tmux target (jsonl-only / view-only sessions).
func (s *liveSession) statusLoop(ctx context.Context) {
	if s.tmuxTarget == "" {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pane, err := s.d.tmux.CapturePane(s.tmuxTarget)
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
		select {
		case s.d.outbound <- wire.Frame{
			Type: wire.FrameTypeSessionStatus,
			Payload: wire.SessionStatus{
				SessionID: s.cwd,
				Status:    status,
				Preview:   preview,
				Meta:      meta,
			},
		}:
		case <-ctx.Done():
			return
		}
	}
}

// HandleHistory replies with the last N records from the jsonl, paced so
// the relay's fanout outbox doesn't drop frames.
func (s *liveSession) HandleHistory(req wire.SessionHistoryReq) {
	path := s.currentJSONLPath()
	if path == "" {
		log.Printf("session %s: history requested but no jsonl yet", s.cwd)
		return
	}
	n := req.Last
	if n <= 0 {
		n = 50
	}
	if n > 200 {
		n = 200 // cap to keep the relay buffer happy
	}
	recs, err := jsonltail.LastN(path, n)
	if err != nil {
		log.Printf("session %s: history read failed: %v", s.cwd, err)
		return
	}
	log.Printf("session %s: replying with %d history records", s.cwd, len(recs))
	for _, rec := range recs {
		select {
		case s.d.outbound <- wire.Frame{
			Type: wire.FrameTypeSessionMessage,
			Payload: wire.SessionMessage{
				SessionID: s.cwd,
				Msg: wire.Message{
					Role:    rec.Role,
					Content: rec.Content,
					TS:      rec.TS,
					ID:      rec.UUID,
				},
			},
		}:
		default:
			// Outbound buffer is full — log and bail. Phone can re-request.
			log.Printf("session %s: outbound full during history send; aborting", s.cwd)
			return
		}
		// pace the burst so the relay's fanout outbox (64 slots) doesn't drop
		time.Sleep(10 * time.Millisecond)
	}
}

// HandleSend pushes phone-typed text into the tmux pane. No-op if this
// session has no associated pane (shouldn't happen in production since
// scan only registers cwds with claude panes, but guard anyway).
func (s *liveSession) HandleSend(req wire.SessionSend) {
	if s.tmuxTarget == "" {
		log.Printf("session %s: send dropped — no tmux target", s.cwd)
		return
	}
	if err := s.d.tmux.SendLine(s.tmuxTarget, req.Text); err != nil {
		log.Printf("session %s: send-keys failed: %v", s.cwd, err)
	}
}

// HandleInterrupt sends Esc to the tmux pane — Claude Code's "stop the
// current generation / dismiss prompt" key.
func (s *liveSession) HandleInterrupt(_ wire.SessionInterrupt) {
	if s.tmuxTarget == "" {
		log.Printf("session %s: interrupt dropped — no tmux target", s.cwd)
		return
	}
	if err := s.d.tmux.SendEscape(s.tmuxTarget); err != nil {
		log.Printf("session %s: send-keys Escape failed: %v", s.cwd, err)
	}
}

// HandleASR transcribes a phone-recorded clip via Bailian. Runs in a
// goroutine so the dispatch loop isn't blocked by network calls. The
// session_id on the result frame mirrors the request (so the phone can
// route the transcript back to the right chat).
func (s *liveSession) HandleASR(req wire.ASRRequest) {
	if s.d.asrClient == nil {
		s.sendASRResult(wire.ASRResult{
			RequestID: req.RequestID,
			Error:     "ASR not configured on agent (missing BAILIAN_API_KEY)",
		})
		return
	}
	go s.runASR(req)
}

// sendASRResult emits an ASRResult frame, but won't block forever: if the
// outbound channel is saturated and the daemon ctx is canceled, the result
// is dropped so the goroutine doesn't leak after outboundLoop has exited.
func (s *liveSession) sendASRResult(result wire.ASRResult) {
	frame := wire.Frame{Type: wire.FrameTypeASRResult, Payload: result}
	select {
	case s.d.outbound <- frame:
	case <-s.ctx.Done():
		log.Printf("session %s: dropped ASR result on shutdown (req=%s)", s.cwd, result.RequestID)
	}
}

func (s *liveSession) runASR(req wire.ASRRequest) {
	result := wire.ASRResult{RequestID: req.RequestID}
	defer s.sendASRResult(result)

	audio, err := stdBase64.StdEncoding.DecodeString(req.AudioB64)
	if err != nil {
		result.Error = fmt.Sprintf("decode audio: %v", err)
		return
	}
	log.Printf("session %s: asr transcribing %d bytes (format=%s, req=%s)",
		s.cwd, len(audio), req.Format, req.RequestID)
	transcript, err := s.d.asrClient.Transcribe(audio, req.Format)
	if err != nil {
		log.Printf("session %s: asr transcribe failed: %v", s.cwd, err)
		result.Error = err.Error()
		return
	}
	log.Printf("session %s: asr raw → %q", s.cwd, truncateStr(transcript, 80))
	cleaned, err := s.d.asrClient.Normalize(transcript)
	if err != nil {
		log.Printf("session %s: asr normalize failed, using raw: %v", s.cwd, err)
		cleaned = transcript
	} else if cleaned != transcript {
		log.Printf("session %s: asr cleaned → %q", s.cwd, truncateStr(cleaned, 80))
	}
	result.Transcript = cleaned
}

// ---------------------------------------------------------------------
// Pure helpers — TUI scrapers, copied verbatim from the old daemon.
// ---------------------------------------------------------------------

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

func truncateStr(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "…"
}
