package jsonltail

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// Tailer polls a file for new lines and emits parsed records. It starts at
// the beginning of the file, which is correct on first attach — the caller
// can skip historical records if undesired.
type Tailer struct {
	path     string
	interval time.Duration
}

func NewTailer(path string, interval time.Duration) *Tailer {
	return &Tailer{path: path, interval: interval}
}

func (t *Tailer) Run(ctx context.Context, out chan<- Record) error {
	var (
		offset int64
		ticker = time.NewTicker(t.interval)
	)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := t.readNewLines(offset, out, ctx)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		offset += n

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (t *Tailer) readNewLines(offset int64, out chan<- Record, ctx context.Context) (int64, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek: %w", err)
	}

	var read int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		read += int64(len(line)) + 1 // +1 for newline
		rec, ok, err := ParseLine(line)
		if err != nil {
			continue
		}
		if !ok {
			continue
		}
		select {
		case <-ctx.Done():
			return read, ctx.Err()
		case out <- rec:
		}
	}
	return read, sc.Err()
}
