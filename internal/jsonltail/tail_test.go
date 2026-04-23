package jsonltail

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTail_EmitsHistoryThenNewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":"old"},"uuid":"u0","sessionId":"s1"}`+"\n"), 0644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := NewTailer(path, 50*time.Millisecond)
	tailer.StartFromBeginning = true
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

func TestTail_SkipsHistoryByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	// Pre-existing "history" line that should NOT be emitted.
	os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":"history"},"uuid":"old","sessionId":"s1"}`+"\n"), 0644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := NewTailer(path, 50*time.Millisecond)
	out := make(chan Record, 10)
	go func() { _ = tailer.Run(ctx, out) }()

	// Give the tailer a moment to start.
	time.Sleep(150 * time.Millisecond)

	// Nothing should have been emitted yet.
	select {
	case rec := <-out:
		t.Fatalf("expected no history replay, but got record %s", rec.UUID)
	default:
	}

	// Append a new line → should be emitted.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"user","message":{"role":"user","content":"live"},"uuid":"new","sessionId":"s1"}` + "\n")
	f.Close()

	select {
	case rec := <-out:
		assert.Equal(t, "new", rec.UUID)
	case <-time.After(1 * time.Second):
		t.Fatal("expected live record after append")
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
