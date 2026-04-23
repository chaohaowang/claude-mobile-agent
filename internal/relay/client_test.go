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

	c := New(wsURL)
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

func TestClient_ReconnectsAfterDisconnect(t *testing.T) {
	srv, _, _ := startEchoServer(t)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	c := New(wsURL)
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
