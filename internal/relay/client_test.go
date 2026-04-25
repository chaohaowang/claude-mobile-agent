package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"

	"github.com/chaohaow/claude-mobile-agent/internal/wire"
)

func startEchoServer(t *testing.T) (*httptest.Server, *[]wire.Frame, *sync.Mutex) {
	upg := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var got []wire.Frame
	var mu sync.Mutex

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
			if err := json.Unmarshal(data, &f); err == nil {
				mu.Lock()
				got = append(got, f)
				mu.Unlock()
			}
			c.WriteMessage(websocket.TextMessage, data)
		}
	}))
	return srv, &got, &mu
}

func TestClient_SendAndReceive(t *testing.T) {
	srv, got, mu := startEchoServer(t)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	c := New(wsURL, "p1", "agent", "devA")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	assert.NoError(t, c.Connect(ctx))

	frame := wire.Frame{
		Type:    wire.FrameTypeSessionStatus,
		Seq:     1,
		Payload: wire.SessionStatus{SessionID: "s1", Status: "idle"},
	}
	assert.NoError(t, c.Send(frame))

	select {
	case f := <-c.Incoming():
		assert.Equal(t, wire.FrameTypeSessionStatus, f.Type)
		assert.Equal(t, uint64(1), f.Seq)
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive echoed frame")
	}

	mu.Lock()
	assert.Len(t, *got, 1)
	mu.Unlock()
}

// TestClient_SendsPings asserts the client emits WebSocket control pings at
// the configured cadence. Without these, idle WS gets silently dropped by
// upstream LBs/NAT after ~10 minutes (Alibaba SLB in our case).
func TestClient_SendsPings(t *testing.T) {
	upg := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var pings int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upg.Upgrade(w, r, nil)
		assert.NoError(t, err)
		defer c.Close()
		c.SetPingHandler(func(appData string) error {
			mu.Lock()
			pings++
			mu.Unlock()
			return c.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
		})
		// Drain frames so ReadMessage drives the ping handler.
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	c := New(wsURL, "p1", "agent", "devA")
	c.PingInterval = 40 * time.Millisecond
	c.PongTimeout = 1 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	assert.NoError(t, c.Connect(ctx))

	// Wait long enough to see at least 3 pings.
	time.Sleep(220 * time.Millisecond)
	mu.Lock()
	got := pings
	mu.Unlock()
	assert.GreaterOrEqual(t, got, 3, "expected at least 3 pings, got %d", got)
}

func TestClient_ReconnectsAfterDisconnect(t *testing.T) {
	srv, _, _ := startEchoServer(t)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	c := New(wsURL, "p1", "agent", "devA")
	c.ReconnectInitial = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	assert.NoError(t, c.Connect(ctx))

	srv.Close()
	time.Sleep(200 * time.Millisecond)

	cancel()
	err := c.Send(wire.Frame{Type: wire.FrameTypePing, Seq: 99})
	assert.Error(t, err)
}
