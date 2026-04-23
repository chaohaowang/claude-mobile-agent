package jsonltail

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/chaohaow/claude-mobile-agent/internal/wire"
)

// Record is a normalized message extracted from a Claude Code jsonl line.
type Record struct {
	UUID       string
	ParentUUID string
	Role       string // "user" | "assistant"
	Content    []wire.ContentBlock
	TS         int64
	SessionID  string
	CWD        string
}

// raw mirrors the subset of Claude Code's jsonl record shape we care about.
type raw struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	Timestamp  string          `json:"timestamp"`
	SessionID  string          `json:"sessionId"`
	CWD        string          `json:"cwd"`
	Message    json.RawMessage `json:"message"`
}

// ParseLine parses a single jsonl line. Returns (rec, true) for user/assistant
// messages, (zero, false) for meta lines, and an error for malformed JSON.
func ParseLine(line []byte) (Record, bool, error) {
	var r raw
	if err := json.Unmarshal(line, &r); err != nil {
		return Record{}, false, fmt.Errorf("parse line: %w", err)
	}
	if r.Type != "user" && r.Type != "assistant" {
		return Record{}, false, nil
	}

	content, err := extractContent(r.Message)
	if err != nil {
		return Record{}, false, fmt.Errorf("extract content (uuid=%s): %w", r.UUID, err)
	}

	ts, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	return Record{
		UUID:       r.UUID,
		ParentUUID: r.ParentUUID,
		Role:       r.Type,
		Content:    content,
		TS:         ts.UnixMilli(),
		SessionID:  r.SessionID,
		CWD:        r.CWD,
	}, true, nil
}

type messageEnvelope struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func extractContent(msgRaw json.RawMessage) ([]wire.ContentBlock, error) {
	if len(msgRaw) == 0 {
		return nil, nil
	}
	var env messageEnvelope
	if err := json.Unmarshal(msgRaw, &env); err != nil {
		return nil, err
	}
	if len(env.Content) == 0 {
		return nil, nil
	}
	// content may be a plain string (common for user) or an array of blocks
	if env.Content[0] == '"' {
		var s string
		if err := json.Unmarshal(env.Content, &s); err != nil {
			return nil, err
		}
		return []wire.ContentBlock{{Type: "text", Text: s}}, nil
	}

	var blocks []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(env.Content, &blocks); err != nil {
		return nil, err
	}
	out := make([]wire.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		cb := wire.ContentBlock{Type: b.Type, Text: b.Text, Thinking: b.Thinking}
		if b.Type == "tool_use" {
			cb.ToolUse = &wire.ToolUse{ID: b.ID, Name: b.Name, Input: b.Input}
		}
		out = append(out, cb)
	}
	return out, nil
}

// ParseAll reads an entire reader and returns all user/assistant records.
func ParseAll(r io.Reader) ([]Record, error) {
	var out []Record
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		rec, ok, err := ParseLine(sc.Bytes())
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, rec)
		}
	}
	return out, sc.Err()
}
