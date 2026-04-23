package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/chaohaow/claude-mobile-agent/internal/config"
	"github.com/chaohaow/claude-mobile-agent/internal/jsonltail"
	"github.com/chaohaow/claude-mobile-agent/internal/relay"
	"github.com/chaohaow/claude-mobile-agent/internal/sessionmgr"
	"github.com/chaohaow/claude-mobile-agent/internal/tmuxctl"
	"github.com/chaohaow/claude-mobile-agent/internal/wire"
)

type Daemon struct {
	cfg    *config.Config
	tmux   *tmuxctl.Client
	relay  *relay.Client
	seq    atomic.Uint64
	sessID string
}

func New(cfg *config.Config) *Daemon {
	return &Daemon{
		cfg:    cfg,
		tmux:   tmuxctl.New(),
		relay:  relay.New(cfg.Relay.URL),
		sessID: cfg.Session.Name,
	}
}

func (d *Daemon) Run(ctx context.Context) error {
	// 1. sanity: tmux session must exist
	ok, err := d.tmux.HasSession(d.cfg.Session.TmuxTarget)
	if err != nil {
		return fmt.Errorf("tmux check: %w", err)
	}
	if !ok {
		return fmt.Errorf("tmux session %q not found — start it first", d.cfg.Session.TmuxTarget)
	}

	// 2. locate jsonl
	home, _ := os.UserHomeDir()
	projectsRoot := filepath.Join(home, ".claude", "projects")
	jsonlPath, err := sessionmgr.FindActiveJSONL(projectsRoot, d.cfg.Session.CWD)
	if err != nil {
		return fmt.Errorf("locate jsonl: %w", err)
	}
	log.Printf("tailing jsonl: %s", jsonlPath)

	// 3. connect relay
	if err := d.relay.Connect(ctx); err != nil {
		return fmt.Errorf("relay connect: %w", err)
	}

	// 4. start tail goroutine → sends session.message frames
	records := make(chan jsonltail.Record, 64)
	tailer := jsonltail.NewTailer(jsonlPath, 200*time.Millisecond)
	go func() { _ = tailer.Run(ctx, records) }()

	// 5. start inbound-frame handler → tmux send-keys
	go d.handleIncoming(ctx)

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
				text := p.Text
				if !endsWithNewline(text) {
					text += "\n"
				}
				if err := d.tmux.SendText(d.cfg.Session.TmuxTarget, text); err != nil {
					log.Printf("send-keys failed: %v", err)
				}
			case wire.SessionInterrupt:
				if p.SessionID != d.sessID {
					continue
				}
				if err := d.tmux.SendCtrlC(d.cfg.Session.TmuxTarget); err != nil {
					log.Printf("send Ctrl-C failed: %v", err)
				}
			}
		}
	}
}

func endsWithNewline(s string) bool {
	return len(s) > 0 && s[len(s)-1] == '\n'
}
