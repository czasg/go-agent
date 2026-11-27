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

type ResponsesClient struct {
	cfg Config
	hc  *http.Client
}

func NewResponses(cfg Config) *ResponsesClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &ResponsesClient{cfg: cfg, hc: hc}
}

func (c *ResponsesClient) Name() string {
	if c.cfg.Model == "" {
		return "openai-responses"
	}
	return c.cfg.Model
}

func (c *ResponsesClient) Generate(ctx context.Context, req *ga.ModelRequest) (*ga.ModelResponse, error) {
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
	var out responsesResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	return out.toGA()
}

func (c *ResponsesClient) Stream(ctx context.Context, req *ga.ModelRequest) (ga.ModelStream, error) {
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
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return &responsesStream{
		body:  resp.Body,
		sc:    sc,
		calls: map[int]*ga.ToolCallDelta{},
		items: map[string]int{},
		args:  map[int]int{},
		text:  map[string]int{},
	}, nil
}

func (c *ResponsesClient) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/responses", bytes.NewReader(body))
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

func (c *ResponsesClient) payload(req *ga.ModelRequest, stream bool) map[string]any {
	p := map[string]any{
		"model": c.cfg.Model,
		"store": false,
	}
	if req.System != "" {
		p["instructions"] = req.System
	}

	items := make([]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m == nil {
			continue
		}
		items = append(items, responsesInputItems(m)...)
	}
	p["input"] = items

	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			})
		}
		p["tools"] = tools
	}

	for k, v := range req.Params {
		switch k {
		case "max_tokens", "max_completion_tokens":
			p["max_output_tokens"] = v
		case "response_format":
			applyResponsesFormat(p, v)
		case "reasoning_effort":
			p["reasoning"] = mergeReasoningEffort(p, v)
		case "stop", "stream", "stream_options", "messages", "tools", "input", "model":
			continue
		default:
			p[k] = v
		}
	}
	for k, v := range req.Extra {
		switch k {
		case "stream", "messages", "tools", "input", "model":
			continue
		}
		p[k] = v
	}
	if stream {
		p["stream"] = true
	}
	return p
}

func applyResponsesFormat(p map[string]any, v any) {
	rf, ok := v.(map[string]any)
	if !ok {
		return
	}
	format := map[string]any{}
	for k, val := range rf {
		format[k] = val
	}
	if js, ok := rf["json_schema"].(map[string]any); ok {
		format = map[string]any{"type": "json_schema"}
		for k, val := range js {
			format[k] = val
		}
	}
	text, _ := p["text"].(map[string]any)
	if text == nil {
		text = map[string]any{}
	}
	text["format"] = format
	p["text"] = text
}

func mergeReasoningEffort(p map[string]any, v any) any {
	if r, ok := p["reasoning"].(map[string]any); ok {
		out := map[string]any{}
		for k, val := range r {
			out[k] = val
		}
		out["effort"] = v
		return out
	}
	return map[string]any{"effort": v}
}

func responsesInputItems(m *ga.Message) []any {
	switch m.Role {
	case ga.RoleTool:
		item := map[string]any{
			"type":    "function_call_output",
			"call_id": m.ToolCallID,
			"output":  m.Content,
		}
		return []any{item}
	case ga.RoleAssistant:
		var out []any
		if text := assistantText(m); text != "" {
			out = append(out, map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": text}},
			})
		}
		for _, tc := range m.ToolCalls {
			args := strings.TrimSpace(string(tc.Arguments))
			if args == "" {
				args = "{}"
			}
			id := tc.ID
			if id == "" {
				id = "call_" + tc.Name
			}
			out = append(out, map[string]any{
				"type":      "function_call",
				"call_id":   id,
				"name":      tc.Name,
				"arguments": args,
			})
		}
		return out
	case ga.RoleSystem:
		text := m.Text()
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []any{map[string]any{
			"type":    "message",
			"role":    "system",
			"content": []any{map[string]any{"type": "input_text", "text": text}},
		}}
	default:
		if len(m.MultiContent) == 0 {
			if m.Content == "" {
				return nil
			}
			return []any{map[string]any{"type": "message", "role": "user", "content": m.Content}}
		}
		parts := make([]any, 0, len(m.MultiContent)+1)
		if m.Content != "" {
			parts = append(parts, map[string]any{"type": "input_text", "text": m.Content})
		}
		for _, pt := range m.MultiContent {
			switch pt.Type {
			case "image":
				img := map[string]any{"type": "input_image", "image_url": pt.URL}
				if pt.Detail != "" {
					img["detail"] = pt.Detail
				}
				parts = append(parts, img)
			case "text":
				parts = append(parts, map[string]any{"type": "input_text", "text": pt.Text})
			}
		}
		if len(parts) == 0 {
			return nil
		}
		return []any{map[string]any{"type": "message", "role": "user", "content": parts}}
	}
}

func assistantText(m *ga.Message) string {
	var b strings.Builder
	b.WriteString(m.Content)
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

type responsesContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type responsesItem struct {
	ID        string             `json:"id"`
	Type      string             `json:"type"`
	Role      string             `json:"role"`
	Status    string             `json:"status"`
	Content   []responsesContent `json:"content"`
	CallID    string             `json:"call_id"`
	Name      string             `json:"name"`
	Arguments string             `json:"arguments"`
	Summary   []responsesContent `json:"summary"`
}

type responsesUsageJSON struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func (u responsesUsageJSON) toGA() ga.Usage {
	out := ga.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
		CacheReadTokens:  u.InputTokensDetails.CachedTokens,
		ReasoningTokens:  u.OutputTokensDetails.ReasoningTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	return out
}

type responsesResponse struct {
	ID                string              `json:"id"`
	Status            string              `json:"status"`
	Output            []responsesItem     `json:"output"`
	Usage             responsesUsageJSON  `json:"usage"`
	IncompleteDetails *incompleteDetails  `json:"incomplete_details"`
	Error             *responsesErrorJSON `json:"error"`
}

type incompleteDetails struct {
	Reason string `json:"reason"`
}

type responsesErrorJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r responsesResponse) toGA() (*ga.ModelResponse, error) {
	if r.Error != nil && r.Error.Message != "" {
		return nil, errors.New("openai: " + r.Error.Message)
	}
	if len(r.Output) == 0 {
		return nil, errors.New("openai: empty output")
	}
	msg := &ga.Message{Role: ga.RoleAssistant}
	var text strings.Builder
	var thinking strings.Builder
	for _, it := range r.Output {
		switch it.Type {
		case "message":
			for _, c := range it.Content {
				switch c.Type {
				case "output_text":
					text.WriteString(c.Text)
				case "refusal":
					text.WriteString(c.Refusal)
				}
			}
		case "function_call":
			id := it.CallID
			if id == "" {
				id = "call_" + it.Name
			}
			args := strings.TrimSpace(it.Arguments)
			if args == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ga.ToolCall{
				ID:        id,
				Name:      it.Name,
				Arguments: json.RawMessage(args),
			})
		case "reasoning":
			for _, s := range it.Summary {
				if s.Text == "" {
					continue
				}
				if thinking.Len() > 0 {
					thinking.WriteString("\n")
				}
				thinking.WriteString(s.Text)
			}
		}
	}
	msg.Content = text.String()
	if thinking.Len() > 0 {
		msg.MultiContent = append(msg.MultiContent, ga.Part{Type: "thinking", Text: thinking.String()})
	}
	usage := r.Usage.toGA()
	u := usage
	msg.Usage = &u
	finish := responsesFinish(r.Status, r.IncompleteDetails, len(msg.ToolCalls) > 0)
	return &ga.ModelResponse{
		Message:      msg,
		Usage:        usage,
		FinishReason: finish,
		Raw:          r,
	}, nil
}

func responsesFinish(status string, incomplete *incompleteDetails, hasCalls bool) string {
	if status == "incomplete" && incomplete != nil && incomplete.Reason != "" {
		return incomplete.Reason
	}
	if hasCalls {
		return "tool_calls"
	}
	return "stop"
}

type responsesEvent struct {
	Type        string              `json:"type"`
	OutputIndex int                 `json:"output_index"`
	ItemID      string              `json:"item_id"`
	Delta       string              `json:"delta"`
	Arguments   string              `json:"arguments"`
	Item        *responsesItem      `json:"item"`
	Response    *responsesResponse  `json:"response"`
	Error       *responsesErrorJSON `json:"error"`
	Message     string              `json:"message"`
}

type responsesStream struct {
	body  io.ReadCloser
	sc    *bufio.Scanner
	done  bool
	calls map[int]*ga.ToolCallDelta
	items map[string]int
	args  map[int]int
	text  map[string]int
}

func (s *responsesStream) Recv() (*ga.StreamChunk, error) {
	if s.done {
		return nil, io.EOF
	}
	for s.sc.Scan() {
		line := strings.TrimSpace(s.sc.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
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
		var ev responsesEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			s.done = true
			return nil, fmt.Errorf("openai: decode event: %w", err)
		}
		chunk, err := s.handle(ev)
		if err != nil {
			s.done = true
			return nil, err
		}
		if chunk != nil {
			return chunk, nil
		}
	}
	if err := s.sc.Err(); err != nil {
		s.done = true
		return nil, err
	}
	s.done = true
	return nil, io.EOF
}

func (s *responsesStream) handle(ev responsesEvent) (*ga.StreamChunk, error) {
	switch ev.Type {
	case "response.output_item.added":
		if ev.Item == nil {
			return nil, nil
		}
		if ev.Item.ID != "" {
			s.items[ev.Item.ID] = ev.OutputIndex
		}
		if ev.Item.Type != "function_call" {
			return nil, nil
		}
		id := ev.Item.CallID
		if id == "" {
			id = ev.Item.ID
		}
		td := &ga.ToolCallDelta{Index: ev.OutputIndex, ID: id, Name: ev.Item.Name}
		s.calls[ev.OutputIndex] = td
		return &ga.StreamChunk{ToolCalls: []ga.ToolCallDelta{{Index: td.Index, ID: td.ID, Name: td.Name}}}, nil
	case "response.output_text.delta", "response.refusal.delta":
		if ev.Delta == "" {
			return nil, nil
		}
		s.text[ev.ItemID] += len(ev.Delta)
		return &ga.StreamChunk{TextDelta: ev.Delta}, nil
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if ev.Delta == "" {
			return nil, nil
		}
		return &ga.StreamChunk{ThinkingDelta: ev.Delta}, nil
	case "response.function_call_arguments.delta":
		idx, ok := s.index(ev)
		if !ok || ev.Delta == "" {
			return nil, nil
		}
		s.args[idx] += len(ev.Delta)
		return &ga.StreamChunk{ToolCalls: []ga.ToolCallDelta{{
			Index:     idx,
			Arguments: ev.Delta,
		}}}, nil
	case "response.function_call_arguments.done":
		idx, ok := s.index(ev)
		if !ok {
			return nil, nil
		}
		if len(ev.Arguments) <= s.args[idx] {
			return nil, nil
		}
		suffix := ev.Arguments[s.args[idx]:]
		s.args[idx] = len(ev.Arguments)
		return &ga.StreamChunk{ToolCalls: []ga.ToolCallDelta{{
			Index:     idx,
			Arguments: suffix,
		}}}, nil
	case "response.output_item.done":
		return s.itemDone(ev), nil
	case "response.completed", "response.incomplete":
		if ev.Response == nil {
			s.done = true
			return nil, nil
		}
		finish := responsesFinish(ev.Response.Status, ev.Response.IncompleteDetails, len(s.calls) > 0)
		chunk := &ga.StreamChunk{FinishReason: finish}
		if !ev.Response.Usage.empty() {
			u := ev.Response.Usage.toGA()
			chunk.Usage = &u
		}
		s.done = true
		return chunk, nil
	case "response.failed":
		if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
			return nil, errors.New("openai: " + ev.Response.Error.Message)
		}
		return nil, errors.New("openai: response failed")
	case "error":
		if ev.Error != nil && ev.Error.Message != "" {
			return nil, errors.New("openai: " + ev.Error.Message)
		}
		if ev.Message != "" {
			return nil, errors.New("openai: " + ev.Message)
		}
		return nil, errors.New("openai: stream error")
	}
	return nil, nil
}

func (s *responsesStream) itemDone(ev responsesEvent) *ga.StreamChunk {
	if ev.Item == nil {
		return nil
	}
	if ev.Item.ID != "" {
		s.items[ev.Item.ID] = ev.OutputIndex
	}
	switch ev.Item.Type {
	case "function_call":
		idx := ev.OutputIndex
		full := ev.Item.Arguments
		if _, ok := s.calls[idx]; !ok {
			id := ev.Item.CallID
			if id == "" {
				id = ev.Item.ID
			}
			s.calls[idx] = &ga.ToolCallDelta{Index: idx, ID: id, Name: ev.Item.Name}
			s.args[idx] = len(full)
			return &ga.StreamChunk{ToolCalls: []ga.ToolCallDelta{{
				Index:     idx,
				ID:        id,
				Name:      ev.Item.Name,
				Arguments: full,
			}}}
		}
		if len(full) <= s.args[idx] {
			return nil
		}
		suffix := full[s.args[idx]:]
		s.args[idx] = len(full)
		return &ga.StreamChunk{ToolCalls: []ga.ToolCallDelta{{
			Index:     idx,
			Arguments: suffix,
		}}}
	case "message":
		if s.text[ev.Item.ID] > 0 {
			return nil
		}
		var b strings.Builder
		for _, c := range ev.Item.Content {
			switch c.Type {
			case "output_text":
				b.WriteString(c.Text)
			case "refusal":
				b.WriteString(c.Refusal)
			}
		}
		if b.Len() == 0 {
			return nil
		}
		s.text[ev.Item.ID] = b.Len()
		return &ga.StreamChunk{TextDelta: b.String()}
	}
	return nil
}

func (s *responsesStream) index(ev responsesEvent) (int, bool) {
	if _, ok := s.calls[ev.OutputIndex]; ok {
		return ev.OutputIndex, true
	}
	if ev.ItemID != "" {
		if idx, ok := s.items[ev.ItemID]; ok {
			return idx, true
		}
	}
	if len(s.calls) == 1 {
		for idx := range s.calls {
			return idx, true
		}
	}
	return 0, false
}

func (s *responsesStream) Close() error {
	return s.body.Close()
}

func (u responsesUsageJSON) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0 &&
		u.InputTokensDetails.CachedTokens == 0 && u.OutputTokensDetails.ReasoningTokens == 0
}
