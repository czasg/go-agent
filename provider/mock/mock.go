package mock

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/czasg/go-agent"
)

type Model struct {
	name string

	mu        sync.Mutex
	responses []*ga.ModelResponse
	fn        func(*ga.ModelRequest) (*ga.ModelResponse, error)
	i         int
	requests  []*ga.ModelRequest
	err       error
}

func New(responses ...*ga.ModelResponse) *Model {
	return &Model{name: "mock", responses: responses}
}

func NewFunc(fn func(*ga.ModelRequest) (*ga.ModelResponse, error)) *Model {
	return &Model{name: "mock", fn: fn}
}

func NewError(err error) *Model {
	return &Model{name: "mock", err: err}
}

func (m *Model) Name() string {
	if m.name == "" {
		return "mock"
	}
	return m.name
}

func (m *Model) Requests() []*ga.ModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*ga.ModelRequest(nil), m.requests...)
}

func (m *Model) Generate(ctx context.Context, req *ga.ModelRequest) (*ga.ModelResponse, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	fn := m.fn
	err := m.err
	var resp *ga.ModelResponse
	if fn == nil && err == nil {
		if m.i < len(m.responses) {
			resp = m.responses[m.i]
			m.i++
		} else {
			resp = Text("done")
		}
	}
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if fn != nil {
		return fn(req)
	}
	if resp == nil {
		resp = Text("done")
	}
	return cloneResponse(resp), nil
}

func cloneResponse(r *ga.ModelResponse) *ga.ModelResponse {
	out := *r
	out.Message = r.Message.Clone()
	return &out
}

func Text(s string) *ga.ModelResponse {
	return &ga.ModelResponse{
		Message:      ga.AssistantMessage(s),
		FinishReason: "stop",
		Usage:        ga.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

func TextWithUsage(s string, u ga.Usage) *ga.ModelResponse {
	r := Text(s)
	r.Usage = u
	if r.Message != nil {
		u2 := u
		r.Message.Usage = &u2
	}
	return r
}

func Call(id, name, args string) ga.ToolCall {
	if args == "" {
		args = "{}"
	}
	return ga.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func ToolCallResponse(text string, calls ...ga.ToolCall) *ga.ModelResponse {
	m := ga.AssistantMessage(text)
	m.ToolCalls = calls
	return &ga.ModelResponse{
		Message:      m,
		FinishReason: "tool_calls",
		Usage:        ga.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

func Panic(msg string) *ga.ModelResponse {
	panic(msg)
}

type StreamingModel struct {
	mu      sync.Mutex
	chunks  []*ga.StreamChunk
	i       int
	request []*ga.ModelRequest
	err     error
}

func Stream(chunks ...*ga.StreamChunk) *StreamingModel {
	return &StreamingModel{chunks: chunks}
}

func StreamError(err error) *StreamingModel {
	return &StreamingModel{err: err}
}

func (m *StreamingModel) Name() string { return "mock-stream" }

func (m *StreamingModel) Generate(ctx context.Context, req *ga.ModelRequest) (*ga.ModelResponse, error) {
	m.mu.Lock()
	m.request = append(m.request, req)
	m.mu.Unlock()
	return m.build(), nil
}

func (m *StreamingModel) build() *ga.ModelResponse {
	var text string
	var calls []ga.ToolCall
	for _, c := range m.chunks {
		text += c.TextDelta
		for _, td := range c.ToolCalls {
			calls = append(calls, ga.ToolCall{ID: td.ID, Name: td.Name, Arguments: json.RawMessage(td.Arguments)})
		}
	}
	msg := ga.AssistantMessage(text)
	msg.ToolCalls = calls
	return &ga.ModelResponse{Message: msg}
}

func (m *StreamingModel) Stream(ctx context.Context, req *ga.ModelRequest) (ga.ModelStream, error) {
	m.mu.Lock()
	m.request = append(m.request, req)
	err := m.err
	i := m.i
	m.i++
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if i > 0 {
		return &stream{cursor: len(m.chunks)}, nil
	}
	return &stream{owner: m}, nil
}

type stream struct {
	owner  *StreamingModel
	cursor int
}

func (s *stream) Recv() (*ga.StreamChunk, error) {
	if s.owner == nil {
		return nil, io.EOF
	}
	if s.cursor >= len(s.owner.chunks) {
		return nil, io.EOF
	}
	c := s.owner.chunks[s.cursor]
	s.cursor++
	return c, nil
}

func (s *stream) Close() error { return nil }

func TextChunk(s string) *ga.StreamChunk {
	return &ga.StreamChunk{TextDelta: s}
}

func FinalChunk(reason string) *ga.StreamChunk {
	return &ga.StreamChunk{FinishReason: reason}
}

func CallChunk(index int, id, name, args string) *ga.StreamChunk {
	return &ga.StreamChunk{ToolCalls: []ga.ToolCallDelta{{Index: index, ID: id, Name: name, Arguments: args}}}
}
