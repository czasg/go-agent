// Package summary 提供上下文摘要压缩 Hook：当消息历史超过 token 阈值时，
// 调用轻量模型生成摘要，用一条 summary 消息替换旧消息，保证模型上下文不溢出。
//
// 原始消息通过 SummaryEvent 交给业务层，业务层自行决定如何备份/持久化。
//
// 用法：
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithHooks(summary.New(
//	        summary.WithSummaryModel(model),   // 用于生成摘要的模型（可选，默认用 agent 的模型）
//	        summary.WithTokenLimit(80000),      // 触发摘要的 token 阈值（默认 100000）
//	        summary.WithKeepRecentRounds(3),    // 保留最近 3 轮原始对话（默认 3）
//	    )),
//	)
package summary

import (
	"fmt"
	"strings"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/llm"
	"github.com/czasg/go-agent/schema"
)

// SummaryEvent 摘要事件类型。业务层通过 onEvent 消费。
const SummaryEvent ga.EventType = "summary"

// SummaryData 摘要事件携带的数据。
type SummaryData struct {
	ReplacedMessages []*schema.Message // 被替换的原始消息（业务层自行备份）
	Summary          string            // 生成的摘要文本
}

// ── 配置 ──────────────────────────────────────────────────────

type Option func(*SummaryHook)

// WithTokenLimit 设置触发摘要的 token 阈值。默认 100000。
func WithTokenLimit(n int) Option {
	return func(h *SummaryHook) { h.tokenLimit = n }
}

// WithKeepRecentRounds 设置保留最近 N 轮原始对话。默认 3。
func WithKeepRecentRounds(n int) Option {
	return func(h *SummaryHook) { h.keepRecentRounds = n }
}

// WithSummaryModel 设置用于生成摘要的模型。默认使用 Agent 的模型。
func WithSummaryModel(m llm.BaseModel) Option {
	return func(h *SummaryHook) { h.model = m }
}

// ── Hook 实现 ─────────────────────────────────────────────────

// SummaryHook 实现 IterationStartHook，在每次调用模型前检查上下文长度，
// 超过阈值时自动压缩历史。
type SummaryHook struct {
	model            llm.BaseModel
	tokenLimit       int
	keepRecentRounds int
}

// New 创建 SummaryHook。
func New(opts ...Option) *SummaryHook {
	h := &SummaryHook{
		tokenLimit:       100000,
		keepRecentRounds: 3,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *SummaryHook) Name() string { return "summary" }

func (h *SummaryHook) OnIterationStart(c *ga.Context) {
	total := c.Messages.TotalTokens()
	if total < h.tokenLimit {
		return
	}

	// 提取最近 N 轮之前的旧轮次。
	extracted := c.Messages.ExtractBeforeRounds(h.keepRecentRounds)
	if extracted == nil || len(extracted.Messages) < 2 {
		return
	}

	// 调模型生成摘要。
	summary, err := h.summarize(c, extracted.Messages)
	if err != nil {
		c.Emit(ga.IterationEndEvent, fmt.Errorf("summary failed: %w", err))
		return
	}

	// 删除旧消息，插入摘要消息。
	checkpoint := &schema.Message{
		Role:    schema.User,
		Content: summary,
		Summary: true,
		Extra: map[string]any{
			"replaced_count": len(extracted.Messages),
		},
	}

	c.Messages.Delete(extracted.Start, extracted.End)
	c.Messages.Insert(extracted.Start, checkpoint)

	// 通知业务层。
	c.Emit(ga.MessageEndEvent, checkpoint)
	c.Emit(SummaryEvent, &SummaryData{
		ReplacedMessages: extracted.Messages,
		Summary:          summary,
	})
}

// summarize 调模型生成摘要。直接 Chat，不走 agent loop。
func (h *SummaryHook) summarize(c *ga.Context, msgs []*schema.Message) (string, error) {
	model := h.model
	if model == nil {
		model = c.Agent.ChatModel()
	}

	var buf strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case schema.User:
			fmt.Fprintf(&buf, "User: %s\n", m.Content)
		case schema.Assistant:
			fmt.Fprintf(&buf, "Assistant: %s\n", m.Content)
		case schema.Tool:
			fmt.Fprintf(&buf, "[Tool %s]: %s\n", m.ToolName, m.Content)
		}
	}

	prompt := fmt.Sprintf("请将以下对话压缩为简明摘要，保留关键信息、决策和上下文。直接输出摘要，不要加前缀。\n\n%s", buf.String())

	resp, err := model.Chat(c, []*schema.Message{
		{Role: schema.User, Content: prompt},
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

