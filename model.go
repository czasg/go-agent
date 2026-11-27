package ga

import (
	"context"
	"encoding/json"
)

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

func (u Usage) Add(o Usage) Usage {
	return Usage{
		PromptTokens:     u.PromptTokens + o.PromptTokens,
		CompletionTokens: u.CompletionTokens + o.CompletionTokens,
		TotalTokens:      u.TotalTokens + o.TotalTokens,
		CacheReadTokens:  u.CacheReadTokens + o.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + o.CacheWriteTokens,
		ReasoningTokens:  u.ReasoningTokens + o.ReasoningTokens,
	}
}

func (u Usage) Zero() bool {
	return u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0 &&
		u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 && u.ReasoningTokens == 0
}

func (u Usage) CacheHitRate() float64 {
	if u.PromptTokens <= 0 {
		return 0
	}
	r := float64(u.CacheReadTokens) / float64(u.PromptTokens)
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

const requestOverhead = 3

type ModelRequest struct {
	System   string
	Messages []*Message
	Tools    []ToolSpec
	Params   map[string]any
	Extra    map[string]any
}

func (r *ModelRequest) EstimateTokens() int {
	if r == nil {
		return 0
	}
	n := requestOverhead + textTokens(r.System)
	for _, m := range r.Messages {
		n += m.EstimateTokens()
	}
	if len(r.Tools) > 0 {
		n += toolSpecTokens(r.Tools)
	}
	return n
}

func toolSpecTokens(specs []ToolSpec) int {
	if len(specs) == 0 {
		return 0
	}
	raw, err := json.Marshal(specs)
	if err != nil {
		return 0
	}
	return textTokens(string(raw))
}

type ModelResponse struct {
	Message      *Message
	Usage        Usage
	FinishReason string
	Raw          any
}

type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type StreamChunk struct {
	TextDelta     string
	ThinkingDelta string
	ToolCalls     []ToolCallDelta
	FinishReason  string
	Usage         *Usage
}

type Model interface {
	Name() string
	Generate(ctx context.Context, req *ModelRequest) (*ModelResponse, error)
}

type ModelStream interface {
	Recv() (*StreamChunk, error)
	Close() error
}

type StreamModel interface {
	Model
	Stream(ctx context.Context, req *ModelRequest) (ModelStream, error)
}

type ModelMiddleware func(next Model) Model

func UseModel(m Model, mws ...ModelMiddleware) Model {
	if m == nil {
		return nil
	}
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] == nil {
			continue
		}
		m = mws[i](m)
	}
	return m
}
