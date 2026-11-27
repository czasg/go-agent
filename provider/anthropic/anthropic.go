package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/czasg/go-agent"
)

type Config struct {
	APIKey       string
	BaseURL      string
	Model        string
	MaxTokens    int
	HTTPClient   *http.Client
	Headers      map[string]string
	AnthropicVer string
}

type Client struct {
	cfg Config
	hc  *http.Client
}

func New(cfg Config) *Client {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.AnthropicVer = strings.TrimSpace(cfg.AnthropicVer)
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.AnthropicVer == "" {
		cfg.AnthropicVer = "2023-06-01"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.Headers == nil {
		cfg.Headers = map[string]string{}
	}
	for k, v := range cfg.Headers {
		cfg.Headers[k] = strings.TrimSpace(v)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{cfg: cfg, hc: hc}
}

func (c *Client) Name() string {
	if c.cfg.Model == "" {
		return "anthropic"
	}
	return c.cfg.Model
}

func (c *Client) Generate(ctx context.Context, req *ga.ModelRequest) (*ga.ModelResponse, error) {
	body, err := json.Marshal(c.payload(req, false))
	if err != nil {
		return nil, err
	}
	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, truncate(string(data), 600))
	}
	var out messageResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("anthropic: decode: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, errors.New("anthropic: " + out.Error.Message)
	}
	return out.toGA()
}

const maxScanToken = 4 << 20

func newScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxScanToken)
	return sc
}

func (c *Client) Stream(ctx context.Context, req *ga.ModelRequest) (ga.ModelStream, error) {
	body, err := json.Marshal(c.payload(req, true))
	if err != nil {
		return nil, err
	}
	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, truncate(string(data), 600))
	}
	return &sseStream{body: resp.Body, sc: newScanner(resp.Body)}, nil
}

func (c *Client) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", c.cfg.AnthropicVer)
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("x-api-key", c.cfg.APIKey)
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	for k, v := range c.cfg.Headers {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

func (c *Client) payload(req *ga.ModelRequest, stream bool) map[string]any {
	p := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": c.cfg.MaxTokens,
	}
	if req.System != "" {
		p["system"] = req.System
	}

	msgs := make([]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m == nil {
			continue
		}
		msgs = append(msgs, toAnthropicMessages(m)...)
	}
	p["messages"] = mergeMessageRuns(msgs)

	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": params,
			})
		}
		p["tools"] = tools
	}

	for k, v := range req.Params {
		switch k {
		case "temperature", "top_p", "top_k", "stop_sequences":
			p[k] = v
		case "max_tokens":
			p["max_tokens"] = v
		case "stop":
			p["stop_sequences"] = v
		}
	}
	for k, v := range req.Extra {
		p[k] = v
	}
	if stream {
		p["stream"] = true
	}
	return p
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    *imageSource    `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

func toAnthropicMessages(m *ga.Message) []any {
	switch m.Role {
	case ga.RoleTool:
		block := map[string]any{
			"type":        "tool_result",
			"tool_use_id": m.ToolCallID,
			"content":     m.Content,
		}
		if v, ok := m.Meta["is_error"]; ok && v == true {
			block["is_error"] = true
		}
		return []any{map[string]any{"role": "user", "content": []any{block}}}
	case ga.RoleAssistant:
		var blocks []any
		if m.Content != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
		}
		for _, p := range m.MultiContent {
			if p.Type == "text" && p.Text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			}
		}
		for _, tc := range m.ToolCalls {
			args := strings.TrimSpace(string(tc.Arguments))
			if args == "" {
				args = "{}"
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Name,
				"input": json.RawMessage(args),
			})
		}
		if len(blocks) == 0 {
			blocks = append(blocks, map[string]any{"type": "text", "text": " "})
		}
		return []any{map[string]any{"role": "assistant", "content": blocks}}
	default:
		if len(m.MultiContent) == 0 {
			return []any{map[string]any{"role": "user", "content": m.Content}}
		}
		var blocks []any
		if m.Content != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
		}
		for _, p := range m.MultiContent {
			switch p.Type {
			case "image":
				blocks = append(blocks, map[string]any{
					"type":   "image",
					"source": map[string]any{"type": "url", "url": p.URL},
				})
			case "text":
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			}
		}
		return []any{map[string]any{"role": "user", "content": blocks}}
	}
}

func mergeMessageRuns(msgs []any) []any {
	out := make([]any, 0, len(msgs))
	for _, raw := range msgs {
		cur := raw.(map[string]any)
		if len(out) > 0 {
			last := out[len(out)-1].(map[string]any)
			if last["role"] == cur["role"] && cur["role"] == "user" {
				lastBlocks, ok1 := last["content"].([]any)
				curBlocks, ok2 := cur["content"].([]any)
				if ok1 && ok2 {
					last["content"] = append(lastBlocks, curBlocks...)
					continue
				}
			}
		}
		out = append(out, cur)
	}
	return out
}

type usageJSON struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (u usageJSON) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0
}

func (u usageJSON) toGA() ga.Usage {
	in := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	return ga.Usage{
		PromptTokens:     in,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      in + u.OutputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
	}
}

type messageResponse struct {
	Content    []contentBlock `json:"content"`
	Usage      usageJSON      `json:"usage"`
	StopReason string         `json:"stop_reason"`
	Error      *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (r messageResponse) toGA() (*ga.ModelResponse, error) {
	msg := &ga.Message{Role: ga.RoleAssistant}
	var text strings.Builder
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			id := b.ID
			if id == "" {
				id = "toolu_" + b.Name
			}
			args := strings.TrimSpace(string(b.Input))
			if args == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ga.ToolCall{ID: id, Name: b.Name, Arguments: json.RawMessage(args)})
		}
	}
	msg.Content = text.String()
	usage := r.Usage.toGA()
	u := usage
	msg.Usage = &u
	finish := r.StopReason
	if len(msg.ToolCalls) > 0 && finish == "end_turn" {
		finish = "tool_use"
	}
	return &ga.ModelResponse{Message: msg, Usage: usage, FinishReason: finish, Raw: r}, nil
}

type sseStream struct {
	body      io.ReadCloser
	sc        *bufio.Scanner
	done      bool
	calls     map[int]*ga.ToolCallDelta
	callOrder []int
	pending   []*ga.StreamChunk
	usage     *ga.Usage
}

func (s *sseStream) Recv() (*ga.StreamChunk, error) {
	if len(s.pending) > 0 {
		out := s.pending[0]
		s.pending = s.pending[1:]
		return out, nil
	}
	if s.done {
		return nil, io.EOF
	}
	for s.sc.Scan() {
		line := strings.TrimSpace(s.sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var ev struct {
			Type    string          `json:"type"`
			Index   int             `json:"index"`
			Content json.RawMessage `json:"content_block"`
			Delta   struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message *struct {
				Usage usageJSON `json:"usage"`
			} `json:"message"`
			Usage *usageJSON `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("anthropic: decode event: %w", err)
		}
		if ev.Error != nil && ev.Error.Message != "" {
			s.done = true
			return nil, errors.New("anthropic: " + ev.Error.Message)
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil && !ev.Message.Usage.empty() {
				u := ev.Message.Usage.toGA()
				s.usage = &u
			}
		case "content_block_start":
			var block contentBlock
			if err := json.Unmarshal(ev.Content, &block); err != nil {
				continue
			}
			if block.Type == "tool_use" {
				if s.calls == nil {
					s.calls = map[int]*ga.ToolCallDelta{}
				}
				td := &ga.ToolCallDelta{Index: ev.Index, ID: block.ID, Name: block.Name}
				s.calls[ev.Index] = td
				s.callOrder = append(s.callOrder, ev.Index)
				return &ga.StreamChunk{ToolCalls: []ga.ToolCallDelta{{Index: td.Index, ID: td.ID, Name: td.Name}}}, nil
			}
		case "content_block_delta":
			if ev.Delta.Text != "" {
				return &ga.StreamChunk{TextDelta: ev.Delta.Text}, nil
			}
			if ev.Delta.Thinking != "" {
				return &ga.StreamChunk{ThinkingDelta: ev.Delta.Thinking}, nil
			}
			if ev.Delta.PartialJSON != "" {
				idx := ev.Index
				name := ""
				id := ""
				if td, ok := s.calls[idx]; ok {
					name = td.Name
					id = td.ID
				}
				return &ga.StreamChunk{ToolCalls: []ga.ToolCallDelta{{Index: idx, ID: id, Name: name, Arguments: ev.Delta.PartialJSON}}}, nil
			}
		case "message_delta":
			chunk := &ga.StreamChunk{FinishReason: ev.Delta.StopReason}
			if ev.Usage != nil {
				u := ev.Usage.toGA()
				if s.usage != nil {
					if u.PromptTokens == 0 {
						u.PromptTokens = s.usage.PromptTokens
						u.CacheReadTokens = s.usage.CacheReadTokens
						u.CacheWriteTokens = s.usage.CacheWriteTokens
					}
					if u.CompletionTokens == 0 {
						u.CompletionTokens = s.usage.CompletionTokens
					}
				}
				if u.TotalTokens == 0 {
					u.TotalTokens = u.PromptTokens + u.CompletionTokens
				}
				chunk.Usage = &u
			}
			return chunk, nil
		case "message_stop":
			s.done = true
			return nil, io.EOF
		}
	}
	if err := s.sc.Err(); err != nil {
		s.done = true
		return nil, err
	}
	s.done = true
	return nil, io.EOF
}

func (s *sseStream) Close() error {
	return s.body.Close()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
