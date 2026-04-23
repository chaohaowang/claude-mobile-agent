// Package asr proxies audio blobs from the phone to Bailian / Dashscope's
// qwen3-asr-flash via its OpenAI-compatible ChatCompletions endpoint.
//
// The phone records, base64-encodes, and ships the whole clip in one WebSocket
// frame. The agent calls the ASR endpoint synchronously and returns a
// transcript string. Keeping this as a single function (not streaming) trades
// latency for simplicity — fine for prompt-length utterances.
package asr

import (
	"bytes"
	stdBase64 "encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultEndpoint = "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
const defaultModel = "qwen3-asr-flash"
const defaultNormalizeModel = "qwen-plus"

// normalizePrompt is the system instruction applied to raw ASR transcripts to
// produce something the user would actually want to send as a chat message.
// Keeps meaning, drops filler. Works for both Chinese and English transcripts.
const normalizePrompt = `你是口语转写整理助手。你的任务是把下面这段口语识别结果整理成自然、简洁的表达，便于直接作为聊天消息发送。

规则:
1. 去掉语气词和填充词: 嗯、呃、啊、哦、那个、就是、对对对、然后就、这个、um、uh、like、you know
2. 去掉口吃/重复/自我修正 (例如 "我想想 我觉得" → "我觉得")
3. 补全明显漏掉的标点,修正明显断句错误
4. 保留原意、语气和说话人的用词习惯,不要改写成书面语
5. 不要添加解释、不要加引号、不要翻译
6. 直接输出整理后的文本,不要前缀

输入可能是中文或英文,输出保持同一种语言。`

// Client wraps the Bailian/Dashscope OpenAI-compatible endpoint. The zero value
// is not usable; construct via [New].
type Client struct {
	Endpoint       string
	Model          string
	NormalizeModel string
	APIKey         string
	HTTP           *http.Client
}

// New reads the API key from env (BAILIAN_API_KEY then DASHSCOPE_API_KEY) and
// returns a ready-to-use Client. Returns an error if neither is set.
func New() (*Client, error) {
	key := os.Getenv("BAILIAN_API_KEY")
	if key == "" {
		key = os.Getenv("DASHSCOPE_API_KEY")
	}
	if key == "" {
		return nil, fmt.Errorf("no BAILIAN_API_KEY or DASHSCOPE_API_KEY in environment")
	}
	return &Client{
		Endpoint:       defaultEndpoint,
		Model:          defaultModel,
		NormalizeModel: defaultNormalizeModel,
		APIKey:         key,
		HTTP:           &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Transcribe sends audio (raw bytes) with the given format (e.g. "m4a", "mp3",
// "wav") to the ASR model and returns the recognized text. Format is used to
// build the data URI the model expects.
func (c *Client) Transcribe(audio []byte, format string) (string, error) {
	if format == "" {
		format = "wav"
	}
	mime := "audio/" + strings.ToLower(format)
	// Dashscope's OpenAI-compatible ASR accepts the audio as a base64-encoded
	// data URI inside a multimodal message. We follow the same shape qwen-audio
	// uses — `input_audio` with `data` + `format`.
	req := map[string]any{
		"model": c.Model,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{
					"type": "input_audio",
					"input_audio": map[string]any{
						"data":   "data:" + mime + ";base64," + base64OfBytes(audio),
						"format": format,
					},
				},
			},
		}},
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequest("POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("asr http %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}

	// OpenAI-compatible response shape: choices[0].message.content is a string
	// for text-only output (which is what ASR emits).
	var parsed struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("parse response (%s): %w", truncate(string(raw), 200), err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("asr api: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("no choices in response: %s", truncate(string(raw), 200))
	}
	switch v := parsed.Choices[0].Message.Content.(type) {
	case string:
		return strings.TrimSpace(v), nil
	case []any:
		// Some models return content as an array of {type:"text", text:"..."}.
		var sb strings.Builder
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := m["text"].(string); ok {
				sb.WriteString(s)
			}
		}
		return strings.TrimSpace(sb.String()), nil
	default:
		return "", fmt.Errorf("unexpected content type in response: %T", v)
	}
}

// Normalize cleans up a raw ASR transcript by asking a text LLM to strip
// filler words and fix punctuation. Returns the raw transcript unchanged on
// any API failure — we'd rather surface something than lose the utterance.
func (c *Client) Normalize(transcript string) (string, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return "", nil
	}
	req := map[string]any{
		"model": c.NormalizeModel,
		"messages": []map[string]any{
			{"role": "system", "content": normalizePrompt},
			{"role": "user", "content": transcript},
		},
		"temperature": 0.2,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return transcript, fmt.Errorf("marshal normalize req: %w", err)
	}
	httpReq, err := http.NewRequest("POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return transcript, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return transcript, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return transcript, err
	}
	if resp.StatusCode >= 400 {
		return transcript, fmt.Errorf("normalize http %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return transcript, err
	}
	if len(parsed.Choices) == 0 {
		return transcript, fmt.Errorf("no choices in normalize response")
	}
	cleaned := strings.TrimSpace(parsed.Choices[0].Message.Content)
	// Strip quote wrappers the model sometimes adds despite the prompt.
	cleaned = strings.Trim(cleaned, "\"'“”‘’")
	if cleaned == "" {
		return transcript, nil
	}
	return cleaned, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// base64OfBytes is a thin wrapper so Transcribe doesn't depend on encoding/base64
// directly in its signature (keeps testing and mocking easier).
func base64OfBytes(b []byte) string {
	return stdBase64.StdEncoding.EncodeToString(b)
}
