package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/chaohaow/claude-mobile-agent/internal/wire"
)

type Client struct {
	url string

	ReconnectInitial time.Duration
	ReconnectMax     time.Duration

	mu       sync.Mutex
	conn     *websocket.Conn
	closed   bool
	ctx      context.Context
	incoming chan wire.Frame
	errCh    chan error
	writeMu  sync.Mutex // serializes WriteMessage across goroutines
}

func New(base, pairID, role, deviceID string) *Client {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	fullURL := fmt.Sprintf("%s%spair=%s&role=%s&device=%s",
		base, sep,
		url.QueryEscape(pairID),
		url.QueryEscape(role),
		url.QueryEscape(deviceID))
	return &Client{
		url:              fullURL,
		ReconnectInitial: 1 * time.Second,
		ReconnectMax:     60 * time.Second,
		incoming:         make(chan wire.Frame, 64),
		errCh:            make(chan error, 1),
	}
}

// Incoming returns the channel of received frames. Closed when the client shuts down.
func (c *Client) Incoming() <-chan wire.Frame { return c.incoming }

// Connect dials the relay and starts a background loop that reads frames.
// It reconnects with exponential backoff until the context is cancelled.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
	if err := c.dial(ctx); err != nil {
		return err
	}
	go c.run(ctx)
	return nil
}

func (c *Client) dial(ctx context.Context) error {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.url, err)
	}
	c.mu.Lock()
	c.conn = conn
	c.closed = false
	c.mu.Unlock()
	return nil
}

func (c *Client) run(ctx context.Context) {
	backoff := c.ReconnectInitial
	for {
		c.readLoop(ctx)

		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}
		c.closed = true
		c.mu.Unlock()

		if ctx.Err() != nil {
			close(c.incoming)
			return
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			close(c.incoming)
			return
		case <-timer.C:
		}
		backoff *= 2
		if backoff > c.ReconnectMax {
			backoff = c.ReconnectMax
		}
		if err := c.dial(ctx); err != nil {
			continue
		}
		backoff = c.ReconnectInitial
	}
}

func (c *Client) readLoop(ctx context.Context) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var f wire.Frame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		select {
		case c.incoming <- f:
		case <-ctx.Done():
			return
		}
	}
}

// Send writes one frame to the connection. Safe to call from multiple
// goroutines; writes are serialized via an internal mutex (gorilla/websocket
// forbids concurrent WriteMessage calls on the same conn).
func (c *Client) Send(f wire.Frame) error {
	c.mu.Lock()
	conn := c.conn
	closed := c.closed
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil && ctx.Err() != nil {
		return errors.New("not connected")
	}
	if conn == nil || closed {
		return errors.New("not connected")
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, data)
}
