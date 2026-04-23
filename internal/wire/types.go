package wire

import (
	"encoding/json"
	"fmt"
)

type FrameType string

const (
	FrameTypeSessionList       FrameType = "session.list"
	FrameTypeSessionMessage    FrameType = "session.message"
	FrameTypeSessionStatus     FrameType = "session.status"
	FrameTypeSessionSend       FrameType = "session.send"
	FrameTypeSessionInterrupt  FrameType = "session.interrupt"
	FrameTypeSessionHistoryReq FrameType = "session.history.req"
	FrameTypeAck               FrameType = "ack"
	FrameTypeError             FrameType = "error"
	FrameTypePing              FrameType = "ping"
)

type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ToolUse  *ToolUse        `json:"tool_use,omitempty"`
	// tool_result fields (flat, matching Claude Code's jsonl shape)
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

type ToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
	TS      int64          `json:"ts"`
	ID      string         `json:"id"`
}

type SessionInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CWD        string `json:"cwd"`
	LastMsgTS  int64  `json:"last_msg_ts"`
	Status     string `json:"status"`
}

type SessionList struct {
	Sessions []SessionInfo `json:"sessions"`
}

type SessionMessage struct {
	SessionID string  `json:"session_id"`
	Msg       Message `json:"msg"`
}

type SessionStatus struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	// Preview: the pane's current "being generated" content (raw tmux
	// capture, ~25 lines above the spinner). Empty when Claude is idle.
	Preview string `json:"preview,omitempty"`
}

type SessionSend struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	IsSlash   bool   `json:"is_slash"`
	RequestID string `json:"request_id"`
}

type SessionInterrupt struct {
	SessionID string `json:"session_id"`
}

// SessionHistoryReq is sent from the phone asking for the last N jsonl
// records. The agent replies with that many session.message frames.
type SessionHistoryReq struct {
	SessionID string `json:"session_id"`
	Last      int    `json:"last"`
}

type Ack struct {
	ForSeq uint64 `json:"for_seq"`
}

type ErrorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

type Frame struct {
	Type    FrameType `json:"type"`
	Seq     uint64    `json:"seq"`
	Payload any       `json:"-"`
}

type rawFrame struct {
	Type    FrameType       `json:"type"`
	Seq     uint64          `json:"seq"`
	Payload json.RawMessage `json:"payload"`
}

func (f Frame) MarshalJSON() ([]byte, error) {
	rf := struct {
		Type    FrameType `json:"type"`
		Seq     uint64    `json:"seq"`
		Payload any       `json:"payload"`
	}{f.Type, f.Seq, f.Payload}
	return json.Marshal(rf)
}

func (f *Frame) UnmarshalJSON(data []byte) error {
	var rf rawFrame
	if err := json.Unmarshal(data, &rf); err != nil {
		return err
	}
	f.Type = rf.Type
	f.Seq = rf.Seq

	var payload any
	switch rf.Type {
	case FrameTypeSessionList:
		var p SessionList
		if err := json.Unmarshal(rf.Payload, &p); err != nil {
			return err
		}
		payload = p
	case FrameTypeSessionMessage:
		var p SessionMessage
		if err := json.Unmarshal(rf.Payload, &p); err != nil {
			return err
		}
		payload = p
	case FrameTypeSessionStatus:
		var p SessionStatus
		if err := json.Unmarshal(rf.Payload, &p); err != nil {
			return err
		}
		payload = p
	case FrameTypeSessionSend:
		var p SessionSend
		if err := json.Unmarshal(rf.Payload, &p); err != nil {
			return err
		}
		payload = p
	case FrameTypeSessionInterrupt:
		var p SessionInterrupt
		if err := json.Unmarshal(rf.Payload, &p); err != nil {
			return err
		}
		payload = p
	case FrameTypeSessionHistoryReq:
		var p SessionHistoryReq
		if err := json.Unmarshal(rf.Payload, &p); err != nil {
			return err
		}
		payload = p
	case FrameTypeAck:
		var p Ack
		if err := json.Unmarshal(rf.Payload, &p); err != nil {
			return err
		}
		payload = p
	case FrameTypeError:
		var p ErrorPayload
		if err := json.Unmarshal(rf.Payload, &p); err != nil {
			return err
		}
		payload = p
	case FrameTypePing:
		payload = struct{}{}
	default:
		return fmt.Errorf("unknown frame type: %s", rf.Type)
	}
	f.Payload = payload
	return nil
}
