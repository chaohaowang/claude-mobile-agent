package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	seq       atomic.Uint64
	sessID    string
	jsonlPath string // populated by Run; used by history-req handler
}

func New(cfg *config.Config) *Daemon {
	return &Daemon{
		cfg:    cfg,
		tmux:   tmuxctl.New(),
		relay:  relay.New(cfg.Relay.URL, cfg.Relay.PairID, "agent", cfg.Relay.DeviceID),
		sessID: cfg.Session.Name,
	}
}

func (d *Daemon) Run(ctx context.Context) error {
	// View-only mode: when tmux_target is empty, skip the tmux check and the
	// inbound handler. Outbound (jsonl → phone) still works; phone-sent frames
	// are dropped with a log message. Useful when Claude Code is running in
	// the user's primary terminal (not in tmux) and we just want to mirror
	// its conversation to a phone as a read-only view.
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
		log.Printf("view-only mode: no tmux_target configured; phone messages will be ignored")
	}

	// 2. locate jsonl
	home, _ := os.UserHomeDir()
	projectsRoot := filepath.Join(home, ".claude", "projects")
	jsonlPath, err := sessionmgr.FindActiveJSONL(projectsRoot, d.cfg.Session.CWD)
	if err != nil {
		return fmt.Errorf("locate jsonl: %w", err)
	}
	d.jsonlPath = jsonlPath
	log.Printf("tailing jsonl: %s", jsonlPath)

	// 3. connect relay
	if err := d.relay.Connect(ctx); err != nil {
		return fmt.Errorf("relay connect: %w", err)
	}

	// 4. start tail goroutine → sends session.message frames
	records := make(chan jsonltail.Record, 64)
	tailer := jsonltail.NewTailer(jsonlPath, 200*time.Millisecond)
	go func() { _ = tailer.Run(ctx, records) }()

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
	recs, err := jsonltail.LastN(d.jsonlPath, n)
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
		combined := status + "\x00" + preview
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
			if p, ok := f.Payload.(wire.SessionHistoryReq); ok {
				d.replyHistory(p)
				continue
			}
			log.Printf("view-only: dropping inbound frame type=%s seq=%d", f.Type, f.Seq)
		}
	}
}

