package jsonltail

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chaohaowang/claude-mobile-agent/internal/wire"
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
		Type      string             `json:"type"`
		Text      string             `json:"text"`
		Thinking  string             `json:"thinking"`
		ID        string             `json:"id"`
		Name      string             `json:"name"`
		Input     json.RawMessage    `json:"input"`
		ToolUseID string             `json:"tool_use_id"`
		Content   json.RawMessage    `json:"content"`
		Source    *wire.ImageSource  `json:"source"`
	}
	if err := json.Unmarshal(env.Content, &blocks); err != nil {
		return nil, err
	}
	out := make([]wire.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		cb := wire.ContentBlock{Type: b.Type, Text: b.Text, Thinking: b.Thinking}
		switch b.Type {
		case "tool_use":
			cb.ToolUse = &wire.ToolUse{ID: b.ID, Name: b.Name, Input: b.Input}
		case "tool_result":
			cb.ToolUseID = b.ToolUseID
			cb.Content = b.Content
		case "image":
			cb.Source = b.Source
		}
		out = append(out, cb)
	}
	return out, nil
}

// LastN reads the entire jsonl file and returns the last n user/assistant
// records (or fewer if the file has fewer). Used to seed a new phone client
// with recent history on connect.
func LastN(path string, n int) ([]Record, error) {
	if n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	all, err := ParseAll(f)
	if err != nil {
		return nil, err
	}
	if len(all) <= n {
		return all, nil
	}
	// Keep the last n records, PLUS every user-role plain-text record from
	// before that window — a tool-heavy session can push 99% of the user's
	// actual prompts out of the tail-N window. User text is short (rarely
	// over a few KB each), so even a few hundred prefix entries stay well
	// under the relay outbound buffer (256 frames).
	tail := all[len(all)-n:]
	prefix := all[:len(all)-n]
	var userTexts []Record
	for _, r := range prefix {
		if r.Role == "user" && hasPlainText(r.Content) {
			userTexts = append(userTexts, r)
		}
	}
	out := make([]Record, 0, len(userTexts)+len(tail))
	out = append(out, userTexts...)
	out = append(out, tail...)
	return out, nil
}

func hasPlainText(blocks []wire.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return true
		}
	}
	return false
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
