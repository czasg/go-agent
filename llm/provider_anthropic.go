package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/czasg/go-agent/internal/util"
	"github.com/czasg/go-agent/schema"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

var _ BaseModel = (*AnthropicChatModel)(nil)

type AnthropicChatModel struct {
	client anthropic.Client
	config *Config
}

// ModelName 返回模型名称。
func (m *AnthropicChatModel) ModelName() string { return m.config.Model }

func NewAnthropicChatModel(config *Config) *AnthropicChatModel {
	config.ApplyDefaults()

	opts := []option.RequestOption{
		option.WithAPIKey(config.ApiKey),
	}
	if config.BaseUrl != "" {
		opts = append(opts, option.WithBaseURL(config.BaseUrl))
	}
	if config.MaxRetries > 0 {
		opts = append(opts, option.WithMaxRetries(int(config.MaxRetries)))
	}

	// HTTPClient 优先级：用户自定义 > Headers 注入 > SDK 默认
	switch {
	case config.HTTPClient != nil:
		opts = append(opts, option.WithHTTPClient(config.HTTPClient))
	case len(config.Headers) > 0:
		for k, v := range config.Headers {
			opts = append(opts, option.WithHeader(k, v))
		}
	}

	return &AnthropicChatModel{
		client: anthropic.NewClient(opts...),
		config: config,
	}
}

// buildRequest 合并 Config 默认值与 Options 覆盖值，构建请求参数。
func (c *AnthropicChatModel) buildRequest(opts *Options) anthropic.MessageNewParams {
	req := anthropic.MessageNewParams{
		Model: anthropic.Model(c.config.Model),
	}

	// 注意：Opus 4.7 / 4.8 已移除 sampling 参数（temperature / top_p / top_k），
	// 发送会返回 400。因此这里不默认发送 temperature，仅在显式通过
	// WithTemperature 覆盖时发送（用于 Sonnet 4.6 等仍支持的模型）。
	if opts.Temperature != nil {
		req.Temperature = anthropic.Float(*opts.Temperature)
	}

	maxTokens := util.Coalesce(c.config.MaxTokens, opts.MaxTokens)
	if maxTokens > 0 {
		req.MaxTokens = maxTokens
	}

	if len(opts.Tools) > 0 {
		req.Tools = c.transformTools(opts.Tools)
	}

	return req
}

func (c *AnthropicChatModel) Chat(ctx context.Context, messages []*schema.Message, opts ...Option) (*schema.Message, error) {
	options := &Options{}
	for _, opt := range opts {
		opt(options)
	}

	req := c.buildRequest(options)
	req.Messages, req.System = c.transformMessages(messages)

	stream := c.client.Messages.NewStreaming(ctx, req)
	defer stream.Close()

	// 累积最终消息
	msg := &schema.Message{
		Role:  schema.Assistant,
		Model: c.config.Model,
	}

	// 时间追踪
	timer := newStreamTimer()

	for stream.Next() {
		event := stream.Current()
		switch e := event.AsAny().(type) {
		case anthropic.MessageDeltaEvent:
			// message_delta 携带累计 usage 与 stop_reason（流的收尾事件）
			c.applyUsageDelta(msg, e.Usage)
			if e.Delta.StopReason != "" {
				msg.OriginReason = string(e.Delta.StopReason)
				if e.Delta.StopReason == anthropic.StopReasonMaxTokens {
					msg.Error = "output truncated: max_tokens reached"
					// 工具调用参数可能被截断（JSON 不完整），清理掉防止下游误用。
					msg.ToolCalls = nil
				}
			}

		case anthropic.ContentBlockStartEvent:
			// tool_use 块开始：记录 tool call 的 id / name，参数由后续
			// input_json_delta 增量拼接。
			if e.ContentBlock.Type == "tool_use" {
				idx := int(e.Index)
				c.ensureToolCall(&msg.ToolCalls, idx)
				msg.ToolCalls[idx].ID = e.ContentBlock.ID
				msg.ToolCalls[idx].Type = "function"
				msg.ToolCalls[idx].Function.Name = e.ContentBlock.Name
				c.emitCallback(options, &schema.MessageDelta{ToolCall: msg.ToolCalls[idx]})
			}

		case anthropic.ContentBlockDeltaEvent:
			switch e.Delta.Type {
			case "thinking_delta":
				// 思考内容（开启 thinking 时先于正文返回）
				timer.markFirstToken()
				timer.markReasoningStart()
				msg.ReasoningContent += e.Delta.Thinking
				c.emitCallback(options, &schema.MessageDelta{ReasoningContent: e.Delta.Thinking})

			case "text_delta":
				// 正文内容
				timer.markFirstToken()
				timer.markReasoningEnd()
				timer.markContentStart()
				msg.Content += e.Delta.Text
				c.emitCallback(options, &schema.MessageDelta{Content: e.Delta.Text})

			case "input_json_delta":
				// tool call 参数（JSON 增量拼接）
				idx := int(e.Index)
				c.ensureToolCall(&msg.ToolCalls, idx)
				msg.ToolCalls[idx].Function.Arguments += e.Delta.PartialJSON
				c.emitCallback(options, &schema.MessageDelta{ToolCall: msg.ToolCalls[idx]})
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("stream: %w", err)
	}

	msg.Timing = schema.Timing{
		TotalDuration:     timer.totalDuration().Milliseconds(),
		TimeToFirstToken:  timer.timeToFirstToken().Milliseconds(),
		ReasoningDuration: timer.reasoningDuration().Milliseconds(),
		ContentDuration:   timer.contentDuration().Milliseconds(),
	}

	return msg, nil
}

func (c *AnthropicChatModel) transformMessages(messages []*schema.Message) ([]anthropic.MessageParam, []anthropic.TextBlockParam) {
	var system []anthropic.TextBlockParam
	out := make([]anthropic.MessageParam, 0, len(messages))

	for _, m := range messages {
		switch m.Role {
		case schema.System:
			system = append(system, anthropic.TextBlockParam{Text: m.Content})

		case schema.User:
			if len(m.MultiParts) > 0 {
				blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.MultiParts))
				for _, mp := range m.MultiParts {
					switch mp.Type {
					case "text":
						blocks = append(blocks, anthropic.NewTextBlock(mp.Text))
					case "image_url":
						if mp.Base64 != "" {
							blocks = append(blocks, anthropic.NewImageBlock(anthropic.Base64ImageSourceParam{
								Data:      mp.Base64,
								MediaType: anthropic.Base64ImageSourceMediaType(mp.MIMEType),
							}))
						} else if mp.URL != "" {
							blocks = append(blocks, anthropic.NewImageBlock(anthropic.URLImageSourceParam{
								URL: mp.URL,
							}))
						}
					}
				}
				out = append(out, anthropic.NewUserMessage(blocks...))
			} else {
				out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
			}

		case schema.Assistant:
			blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.ToolCalls)+1)
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				var input any = map[string]any{}
				if tc.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
				}
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: input,
					},
				})
			}
			// 空 assistant 消息（无正文且无 tool call）不追加
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}

		case schema.Tool:
			out = append(out, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false),
			))
		}
	}

	return out, system
}

func (c *AnthropicChatModel) transformTools(tools []schema.ToolInfo) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		schemaParam := anthropic.ToolInputSchemaParam{}
		if t.Parameters != nil {
			if props, ok := t.Parameters["properties"]; ok {
				schemaParam.Properties = props
			}
			if req, ok := t.Parameters["required"].([]any); ok {
				schemaParam.Required = make([]string, 0, len(req))
				for _, r := range req {
					if s, ok := r.(string); ok {
						schemaParam.Required = append(schemaParam.Required, s)
					}
				}
			}
			// properties / required / type 之外的键透传
			for k, v := range t.Parameters {
				switch k {
				case "properties", "required", "type":
					continue
				}
				if schemaParam.ExtraFields == nil {
					schemaParam.ExtraFields = make(map[string]any)
				}
				schemaParam.ExtraFields[k] = v
			}
		}

		tool := anthropic.ToolParam{
			Name:        t.Name,
			InputSchema: schemaParam,
		}
		if t.Description != "" {
			tool.Description = anthropic.String(t.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return out
}

// ensureToolCall 保证 ToolCalls 切片至少含 idx+1 个元素。
func (c *AnthropicChatModel) ensureToolCall(toolCalls *[]*schema.ToolCall, idx int) {
	for len(*toolCalls) <= idx {
		*toolCalls = append(*toolCalls, &schema.ToolCall{})
	}
}

func (c *AnthropicChatModel) applyUsageDelta(msg *schema.Message, u anthropic.MessageDeltaUsage) {
	prompt := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	msg.Usage = schema.TokenUsage{
		PromptTokens:     int(prompt),
		CompletionTokens: int(u.OutputTokens),
		TotalTokens:      int(prompt + u.OutputTokens),
		CachedTokens:     int(u.CacheReadInputTokens),
		ReasoningTokens:  int(u.OutputTokensDetails.ThinkingTokens),
	}
}

func (c *AnthropicChatModel) emitCallback(opts *Options, event *schema.MessageDelta) {
	if opts.OnCallback != nil {
		opts.OnCallback(event)
	}
}
