package jsonltail

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseLine_UserText(t *testing.T) {
	line := []byte(`{"parentUuid":null,"type":"user","message":{"role":"user","content":"hello"},"uuid":"u1","timestamp":"2026-04-19T15:53:38.358Z","sessionId":"s1"}`)
	rec, ok, err := ParseLine(line)
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "user", rec.Role)
	assert.Equal(t, "u1", rec.UUID)
	assert.Equal(t, "s1", rec.SessionID)
	assert.Len(t, rec.Content, 1)
	assert.Equal(t, "text", rec.Content[0].Type)
	assert.Equal(t, "hello", rec.Content[0].Text)
}

func TestParseLine_AssistantToolUse(t *testing.T) {
	line := []byte(`{"parentUuid":"a1","type":"assistant","message":{"id":"m2","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]},"uuid":"a2","timestamp":"2026-04-19T15:53:40.000Z","sessionId":"s1"}`)
	rec, ok, err := ParseLine(line)
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "assistant", rec.Role)
	assert.Len(t, rec.Content, 1)
	assert.Equal(t, "tool_use", rec.Content[0].Type)
	assert.NotNil(t, rec.Content[0].ToolUse)
	assert.Equal(t, "Bash", rec.Content[0].ToolUse.Name)
}

func TestParseLine_MetaSkipped(t *testing.T) {
	line := []byte(`{"type":"permission-mode","permissionMode":"default","sessionId":"s1"}`)
	_, ok, err := ParseLine(line)
	assert.NoError(t, err)
	assert.False(t, ok, "meta line should be skipped")
}

func TestParseLine_MalformedReturnsError(t *testing.T) {
	_, _, err := ParseLine([]byte(`{not json`))
	assert.Error(t, err)
}

func TestParseFile_YieldsAllMessages(t *testing.T) {
	f, err := os.Open("../../testdata/sample.jsonl")
	assert.NoError(t, err)
	defer f.Close()

	recs, err := ParseAll(f)
	assert.NoError(t, err)
	assert.Len(t, recs, 4)
	assert.Equal(t, "user", recs[0].Role)
	assert.Equal(t, "assistant", recs[1].Role)
	assert.Equal(t, "assistant", recs[2].Role)
	assert.Equal(t, "tool_use", recs[2].Content[0].Type)
}
