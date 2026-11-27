package openai

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
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
	Headers    map[string]string
	OrgID      string
}

type Client struct {
	cfg Config
	hc  *http.Client
}

func New(cfg Config) *Client {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.OrgID = strings.TrimSpace(cfg.OrgID)
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
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
		return "openai"
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
		return nil, fmt.Errorf("openai: status %d: %s", resp.StatusCode, truncate(string(data), 600))
	}
	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, errors.New("openai: " + out.Error.Message)
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
		return nil, fmt.Errorf("openai: status %d: %s", resp.StatusCode, truncate(string(data), 600))
	}
	return &sseStream{body: resp.Body, sc: newScanner(resp.Body)}, nil
}

func (c *Client) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	if c.cfg.OrgID != "" {
		httpReq.Header.Set("OpenAI-Organization", c.cfg.OrgID)
	}
	for k, v := range c.cfg.Headers {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

func (c *Client) payload(req *ga.ModelRequest, stream bool) map[string]any {
	p := map[string]any{"model": c.cfg.Model}

	msgs := make([]any, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		if m == nil {
			continue
		}
		msgs = append(msgs, messageJSON(m))
	}
	p["messages"] = msgs

	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  params,
				},
			})
		}
		p["tools"] = tools
	}

	for k, v := range req.Params {
		p[k] = v
	}
	for k, v := range req.Extra {
		p[k] = v
	}
	if stream {
		p["stream"] = true
		p["stream_options"] = map[string]any{"include_usage": true}
	}
	return p
}

func messageJSON(m *ga.Message) map[string]any {
	out := map[string]any{"role": string(m.Role)}

	if m.Role == ga.RoleTool {
		out["content"] = m.Content
		if m.ToolCallID != "" {
			out["tool_call_id"] = m.ToolCallID
		}
		if m.Name != "" {
			out["name"] = m.Name
		}
		return out
	}

	if len(m.MultiContent) > 0 {
		parts := make([]any, 0, len(m.MultiContent)+1)
		if m.Content != "" {
			parts = append(parts, map[string]any{"type": "text", "text": m.Content})
		}
		for _, p := range m.MultiContent {
			switch p.Type {
			case "image":
				img := map[string]any{"url": p.URL}
				if p.Detail != "" {
					img["detail"] = p.Detail
				}
				parts = append(parts, map[string]any{"type": "image_url", "image_url": img})
			case "text":
				parts = append(parts, map[string]any{"type": "text", "text": p.Text})
			}
		}
		out["content"] = parts
	} else {
		out["content"] = m.Content
	}

	if len(m.ToolCalls) > 0 {
		calls := make([]any, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			args := strings.TrimSpace(string(tc.Arguments))
			if args == "" {
				args = "{}"
			}
			calls = append(calls, map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": args,
				},
			})
		}
		out["tool_calls"] = calls
	}
	if m.Name != "" {
		out["name"] = m.Name
	}
	return out
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Role      string  `json:"role"`
			Content   *string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage usageJSON `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type usageJSON struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails   struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u usageJSON) toGA() ga.Usage {
	out := ga.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
	}
	cached := u.PromptTokensDetails.CachedTokens
	if u.PromptCacheHitTokens > 0 {
		cached = u.PromptCacheHitTokens
	}
	out.CacheReadTokens = cached
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	return out
}

func (r chatResponse) toGA() (*ga.ModelResponse, error) {
	if len(r.Choices) == 0 {
		return nil, errors.New("openai: empty choices")
	}
	ch := r.Choices[0]
	msg := &ga.Message{Role: ga.RoleAssistant}
	if ch.Message.Content != nil {
		msg.Content = *ch.Message.Content
	}
	for _, tc := range ch.Message.ToolCalls {
		args := strings.TrimSpace(tc.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		name := tc.Function.Name
		id := tc.ID
		if id == "" {
			id = "call_" + name
		}
		msg.ToolCalls = append(msg.ToolCalls, ga.ToolCall{
			ID:        id,
			Name:      name,
			Arguments: json.RawMessage(args),
		})
	}
	usage := r.Usage.toGA()
	u := usage
	msg.Usage = &u
	return &ga.ModelResponse{
		Message:      msg,
		Usage:        usage,
		FinishReason: ch.FinishReason,
		Raw:          r,
	}, nil
}

type sseStream struct {
	body io.ReadCloser
	sc   *bufio.Scanner
	done bool
}

func (s *sseStream) Recv() (*ga.StreamChunk, error) {
	if s.done {
		return nil, io.EOF
	}
	for s.sc.Scan() {
		line := strings.TrimSpace(s.sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			s.done = true
			return nil, io.EOF
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("openai: decode chunk: %w", err)
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			s.done = true
			return nil, errors.New("openai: " + chunk.Error.Message)
		}
		out := chunk.toGA()
		if out == nil {
			continue
		}
		return out, nil
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

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageJSON `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c chatChunk) toGA() *ga.StreamChunk {
	out := &ga.StreamChunk{}
	if c.Usage != nil {
		u := c.Usage.toGA()
		out.Usage = &u
	}
	if len(c.Choices) == 0 {
		if out.Usage == nil {
			return nil
		}
		return out
	}
	ch := c.Choices[0]
	out.TextDelta = ch.Delta.Content
	out.ThinkingDelta = ch.Delta.ReasoningContent
	out.FinishReason = ch.FinishReason
	for _, tc := range ch.Delta.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ga.ToolCallDelta{
			Index:     tc.Index,
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
