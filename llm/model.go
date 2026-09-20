package llm

import (
	"context"
	"github.com/czasg/go-agent/config"
	"github.com/czasg/go-agent/schema"
)

type BaseModel interface {
	Chat(ctx context.Context, messages []*schema.Message, opts ...Option) (*schema.Message, error)
}

type Options struct {
	Temperature *float64
	MaxTokens   *int64
	Tools       []schema.ToolInfo
	OnCallback  func(delta *schema.MessageDelta)
}

type Option func(opts *Options)

func WithTemperature(v float64) Option {
	return func(opts *Options) { opts.Temperature = &v }
}

func WithMaxTokens(v int64) Option {
	return func(opts *Options) { opts.MaxTokens = &v }
}

func WithTools(tools []schema.ToolInfo) Option {
	return func(opts *Options) { opts.Tools = tools }
}

func WithCallback(fn func(delta *schema.MessageDelta)) Option {
	return func(opts *Options) { opts.OnCallback = fn }
}

// Config 是 config.LLMConfig 的类型别名，保持对外 API 兼容。
type Config = config.LLMConfig
