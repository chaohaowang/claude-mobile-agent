package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"

	"github.com/chaohaow/claude-mobile-agent/internal/config"
	"github.com/chaohaow/claude-mobile-agent/internal/wire"
)

func TestIntegration_JSONLToRelay(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	// 1. set up a fake ~/.claude/projects layout under a temp HOME
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	cwd := "/tmp/itest-demo"
	projDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-itest-demo")
	assert.NoError(t, os.MkdirAll(projDir, 0755))
	jsonlPath := filepath.Join(projDir, "sess.jsonl")
	assert.NoError(t, os.WriteFile(jsonlPath, []byte{}, 0644))

	// 2. start a real tmux session to serve as the target
	tmuxName := "claude-mobile-itest"
	exec.Command("tmux", "kill-session", "-t", tmuxName).Run()
	assert.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", tmuxName, "cat").Run())
	defer exec.Command("tmux", "kill-session", "-t", tmuxName).Run()

	// 3. spin up a tiny WS server that captures what the daemon sends
	var got []wire.Frame
	var mu sync.Mutex
	upg := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upg.Upgrade(w, r, nil)
		assert.NoError(t, err)
		defer c.Close()
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var f wire.Frame
			if json.Unmarshal(data, &f) == nil {
				mu.Lock()
				got = append(got, f)
				mu.Unlock()
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// 4. build a config pointing at our fixtures
	cfg := &config.Config{
		Relay:   config.RelayConfig{URL: wsURL, ReconnectInitialSec: 1, ReconnectMaxSec: 5},
		Session: config.SessionConfig{TmuxTarget: tmuxName, CWD: cwd, Name: "itest"},
	}

	// 5. run the daemon in a goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := New(cfg)
	go func() { _ = d.Run(ctx) }()

	// 6. append a jsonl line and wait for it to show up on the WS side
	time.Sleep(200 * time.Millisecond)
	f, _ := os.OpenFile(jsonlPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"parentUuid":null,"type":"user","message":{"role":"user","content":"hello integration"},"uuid":"uX","timestamp":"2026-04-19T15:53:38.358Z","sessionId":"sX","cwd":"/tmp/itest-demo"}` + "\n")
	f.Close()

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, frame := range got {
			if frame.Type == wire.FrameTypeSessionMessage {
				sm := frame.Payload.(wire.SessionMessage)
				if sm.Msg.Content[0].Text == "hello integration" {
					return true
				}
			}
		}
		return false
	}, 3*time.Second, 100*time.Millisecond, "expected session.message frame to arrive")
}
