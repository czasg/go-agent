package llm

import (
	"context"
	"fmt"
	"github.com/czasg/go-agent/internal/util"
	"github.com/czasg/go-agent/schema"
	"io"
	"net/http"

	"github.com/sashabaranov/go-openai"
)

var _ BaseModel = (*OpenAIChatModel)(nil)

type OpenAIChatModel struct {
	client *openai.Client
	config *Config
}

// ModelName 返回模型名称。
func (m *OpenAIChatModel) ModelName() string { return m.config.Model }

func NewOpenAIChatModel(config *Config) *OpenAIChatModel {
	config.ApplyDefaults()

	cfg := openai.DefaultConfig(config.ApiKey)
	if config.BaseUrl != "" {
		cfg.BaseURL = config.BaseUrl
	}

	// HTTPClient 优先级：用户自定义 > Headers 注入 > 库默认（无超时）
	switch {
	case config.HTTPClient != nil:
		cfg.HTTPClient = config.HTTPClient
	case len(config.Headers) > 0:
		cfg.HTTPClient = &http.Client{
			Transport: &headerTransport{
				Base:    http.DefaultTransport,
				Headers: config.Headers,
			},
		}
	}

	return &OpenAIChatModel{
		client: openai.NewClientWithConfig(cfg),
		config: config,
	}
}

// buildRequest 合并 Config 默认值与 Options 覆盖值，构建请求参数。
func (c *OpenAIChatModel) buildRequest(opts *Options) openai.ChatCompletionRequest {
	req := openai.ChatCompletionRequest{
		Model:  c.config.Model,
		Stream: true,
	}

	temp := util.Coalesce(c.config.Temperature, opts.Temperature)
	if temp > 0 {
		req.Temperature = float32(temp)
	}

	maxTokens := util.Coalesce(c.config.MaxTokens, opts.MaxTokens)
	if maxTokens > 0 {
		req.MaxTokens = int(maxTokens)
	}

	if len(opts.Tools) > 0 {
		req.Tools = c.transformTools(opts.Tools)
	}

	return req
}

func (c *OpenAIChatModel) Chat(ctx context.Context, messages []*schema.Message, opts ...Option) (*schema.Message, error) {
	options := &Options{}
	for _, opt := range opts {
		opt(options)
	}

	req := c.buildRequest(options)
	req.Messages = c.transformMessages(messages)

	stream, err := c.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create stream: %w", err)
	}
	defer stream.Close()

	msg := &schema.Message{
		Role:  schema.Assistant,
		Model: c.config.Model,
	}

	timer := newStreamTimer()

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("stream recv: %w", err)
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta

			if delta.ReasoningContent != "" {
				timer.markFirstToken()
				timer.markReasoningStart()
				msg.ReasoningContent += delta.ReasoningContent
				c.emitCallback(options, &schema.MessageDelta{ReasoningContent: delta.ReasoningContent})
			}

			if delta.Content != "" {
				timer.markFirstToken()
				timer.markReasoningEnd()
				timer.markContentStart()
				msg.Content += delta.Content
				c.emitCallback(options, &schema.MessageDelta{Content: delta.Content})
			}

			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				for len(msg.ToolCalls) <= idx {
					msg.ToolCalls = append(msg.ToolCalls, &schema.ToolCall{})
				}
				slot := msg.ToolCalls[idx]
				if tc.ID != "" {
					slot.ID = tc.ID
				}
				if tc.Type != "" {
					slot.Type = string(tc.Type)
				}
				if tc.Function.Name != "" {
					slot.Function.Name += tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					slot.Function.Arguments += tc.Function.Arguments
				}
				c.emitCallback(options, &schema.MessageDelta{ToolCall: slot})
			}

			if choice.FinishReason != "" {
				msg.OriginReason = string(choice.FinishReason)
				if choice.FinishReason == openai.FinishReasonLength {
					msg.Error = "output truncated: max_tokens reached"
					// 工具调用参数可能被截断（JSON 不完整），清理掉防止下游误用。
					msg.ToolCalls = nil
				}
			}
		}

		c.applyUsageDelta(msg, chunk.Usage)
	}

	msg.Timing = schema.Timing{
		TotalDuration:     timer.totalDuration().Milliseconds(),
		TimeToFirstToken:  timer.timeToFirstToken().Milliseconds(),
		ReasoningDuration: timer.reasoningDuration().Milliseconds(),
		ContentDuration:   timer.contentDuration().Milliseconds(),
	}

	return msg, nil
}

// transformMessages 将统一 Message 转为 go-openai 请求格式。
func (c *OpenAIChatModel) transformMessages(messages []*schema.Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case schema.System:
			out = append(out, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: m.Content,
			})
		case schema.User:
			if len(m.MultiParts) > 0 {
				parts := make([]openai.ChatMessagePart, 0, len(m.MultiParts))
				for _, mp := range m.MultiParts {
					switch mp.Type {
					case "text":
						parts = append(parts, openai.ChatMessagePart{
							Type: openai.ChatMessagePartTypeText,
							Text: mp.Text,
						})
					case "image_url":
						url := mp.URL
						if url == "" && mp.Base64 != "" {
							url = "data:" + mp.MIMEType + ";base64," + mp.Base64
						}
						parts = append(parts, openai.ChatMessagePart{
							Type:     openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{URL: url},
						})
					}
				}
				out = append(out, openai.ChatCompletionMessage{
					Role:         openai.ChatMessageRoleUser,
					MultiContent: parts,
				})
			} else {
				out = append(out, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: m.Content,
				})
			}
		case schema.Assistant:
			msg := openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: m.Content,
			}
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
			out = append(out, msg)
		case schema.Tool:
			out = append(out, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			})
		}
	}
	return out
}

// transformTools 将统一 ToolInfo 转为 go-openai tools 格式。
func (c *OpenAIChatModel) transformTools(tools []schema.ToolInfo) []openai.Tool {
	out := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return out
}

// applyUsageDelta 从流的 usage 填充用量。usage 为 nil 或空值时跳过。
func (c *OpenAIChatModel) applyUsageDelta(msg *schema.Message, u *openai.Usage) {
	if u == nil || u.TotalTokens <= 0 {
		return
	}
	msg.Usage = schema.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.CompletionTokensDetails != nil {
		msg.Usage.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	if u.PromptTokensDetails != nil {
		msg.Usage.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
}

func (c *OpenAIChatModel) emitCallback(opts *Options, event *schema.MessageDelta) {
	if opts.OnCallback != nil {
		opts.OnCallback(event)
	}
}
