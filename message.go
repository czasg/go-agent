package ga

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

const (
	metaEphemeral = "ephemeral"
	metaIsError   = "is_error"
	metaStop      = "stop"
	metaTruncated = "truncated_from"
)

type Part struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	URL      string `json:"url,omitempty"`
	Detail   string `json:"detail,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Message struct {
	ID           string         `json:"id"`
	Role         Role           `json:"role"`
	Content      string         `json:"content,omitempty"`
	MultiContent []Part         `json:"multi_content,omitempty"`
	ToolCalls    []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID   string         `json:"tool_call_id,omitempty"`
	Name         string         `json:"name,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
	Usage        *Usage         `json:"usage,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

var idSeq atomic.Uint64

func newID(prefix string) string {
	n := idSeq.Add(1)
	return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36) + strconv.FormatUint(n, 36)
}

func SystemMessage(s string) *Message {
	return &Message{Role: RoleSystem, Content: s}
}

func UserMessage(s string) *Message {
	return &Message{Role: RoleUser, Content: s}
}

func AssistantMessage(s string) *Message {
	return &Message{Role: RoleAssistant, Content: s}
}

func ToolMessage(callID, name, s string) *Message {
	return &Message{Role: RoleTool, ToolCallID: callID, Name: name, Content: s}
}

func ImagePart(url string) Part {
	return Part{Type: "image", URL: url}
}

func TextPart(s string) Part {
	return Part{Type: "text", Text: s}
}

func (m *Message) Text() string {
	if m == nil {
		return ""
	}
	if len(m.MultiContent) == 0 {
		return m.Content
	}
	var b strings.Builder
	if m.Content != "" {
		b.WriteString(m.Content)
	}
	for _, p := range m.MultiContent {
		if p.Type != "text" || p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

const (
	messageOverhead  = 4
	toolCallOverhead = 4
	imageTokens      = 85
)

func (m *Message) EstimateTokens() int {
	if m == nil {
		return 0
	}
	n := messageOverhead + textTokens(m.Content)
	for _, tc := range m.ToolCalls {
		n += toolCallOverhead + textTokens(tc.Name) + textTokens(string(tc.Arguments))
	}
	for _, p := range m.MultiContent {
		switch p.Type {
		case "text":
			n += textTokens(p.Text)
		case "image":
			n += imageTokens
		}
	}
	return n
}

func textTokens(s string) int {
	if s == "" {
		return 0
	}
	ascii, wide := 0, 0
	for _, r := range s {
		if r < utf8.RuneSelf {
			ascii++
			continue
		}
		wide++
	}
	return (ascii+3)/4 + wide
}

func Tokens(msgs []*Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil || m.Role != RoleAssistant || m.Usage == nil || m.Usage.TotalTokens <= 0 {
			continue
		}
		total := m.Usage.TotalTokens
		for _, later := range msgs[i+1:] {
			total += later.EstimateTokens()
		}
		return total
	}
	total := 0
	for _, m := range msgs {
		total += m.EstimateTokens()
	}
	return total
}

func (m *Message) Empty() bool {
	if m == nil {
		return true
	}
	return m.Content == "" && len(m.MultiContent) == 0 && len(m.ToolCalls) == 0
}

func (m *Message) SetMeta(k string, v any) *Message {
	if m == nil {
		return m
	}
	if m.Meta == nil {
		m.Meta = map[string]any{}
	}
	m.Meta[k] = v
	return m
}

func (m *Message) MetaValue(k string) (any, bool) {
	if m == nil || m.Meta == nil {
		return nil, false
	}
	v, ok := m.Meta[k]
	return v, ok
}

func (m *Message) Ephemeral() bool {
	if _, ok := m.MetaValue(metaEphemeral); ok {
		return true
	}
	return false
}

func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	out := *m
	if len(m.MultiContent) > 0 {
		out.MultiContent = append([]Part(nil), m.MultiContent...)
	}
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = make([]ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			out.ToolCalls[i] = tc
			if tc.Arguments != nil {
				out.ToolCalls[i].Arguments = append(json.RawMessage(nil), tc.Arguments...)
			}
		}
	}
	if m.Meta != nil {
		out.Meta = make(map[string]any, len(m.Meta))
		for k, v := range m.Meta {
			out.Meta[k] = v
		}
	}
	if m.Usage != nil {
		u := *m.Usage
		out.Usage = &u
	}
	return &out
}
