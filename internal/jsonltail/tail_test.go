package jsonltail

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTail_EmitsNewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":"old"},"uuid":"u0","sessionId":"s1"}`+"\n"), 0644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := NewTailer(path, 50*time.Millisecond)
	out := make(chan Record, 10)
	go func() { _ = tailer.Run(ctx, out) }()

	select {
	case rec := <-out:
		assert.Equal(t, "u0", rec.UUID)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected initial record")
	}

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"user","message":{"role":"user","content":"new"},"uuid":"u1","sessionId":"s1"}` + "\n")
	f.Close()

	select {
	case rec := <-out:
		assert.Equal(t, "u1", rec.UUID)
	case <-time.After(1 * time.Second):
		t.Fatal("expected appended record")
	}
}

func TestTail_RespectsCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	os.WriteFile(path, []byte{}, 0644)

	ctx, cancel := context.WithCancel(context.Background())
	tailer := NewTailer(path, 50*time.Millisecond)
	done := make(chan struct{})
	go func() {
		_ = tailer.Run(ctx, make(chan Record, 1))
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("tailer did not exit after cancel")
	}
}
