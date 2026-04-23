package wire

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSessionMessageMarshal(t *testing.T) {
	f := Frame{
		Type: FrameTypeSessionMessage,
		Seq:  7,
		Payload: SessionMessage{
			SessionID: "sess-1",
			Msg: Message{
				Role:    "assistant",
				Content: []ContentBlock{{Type: "text", Text: "hi"}},
				TS:      1714035600,
				ID:      "m1",
			},
		},
	}
	b, err := json.Marshal(f)
	assert.NoError(t, err)

	var out Frame
	assert.NoError(t, out.UnmarshalJSON(b))
	assert.Equal(t, FrameTypeSessionMessage, out.Type)
	assert.Equal(t, uint64(7), out.Seq)

	sm, ok := out.Payload.(SessionMessage)
	assert.True(t, ok, "payload should be SessionMessage, got %T", out.Payload)
	assert.Equal(t, "sess-1", sm.SessionID)
	assert.Equal(t, "assistant", sm.Msg.Role)
	assert.Equal(t, "hi", sm.Msg.Content[0].Text)
}

func TestSessionSendMarshal(t *testing.T) {
	f := Frame{
		Type: FrameTypeSessionSend,
		Seq:  1,
		Payload: SessionSend{
			SessionID: "sess-1",
			Text:      "/review",
			IsSlash:   true,
			RequestID: "r1",
		},
	}
	b, err := json.Marshal(f)
	assert.NoError(t, err)

	var out Frame
	assert.NoError(t, out.UnmarshalJSON(b))
	ss, ok := out.Payload.(SessionSend)
	assert.True(t, ok)
	assert.Equal(t, "/review", ss.Text)
	assert.True(t, ss.IsSlash)
}

func TestAckMarshal(t *testing.T) {
	f := Frame{Type: FrameTypeAck, Seq: 99, Payload: Ack{ForSeq: 98}}
	b, err := json.Marshal(f)
	assert.NoError(t, err)

	var out Frame
	assert.NoError(t, out.UnmarshalJSON(b))
	a := out.Payload.(Ack)
	assert.Equal(t, uint64(98), a.ForSeq)
}
