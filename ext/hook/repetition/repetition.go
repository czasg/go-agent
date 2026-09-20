// Package repetition 提供重复输出检测 Hook：在模型流式输出过程中，
// 实时检测重复模式（如思考过程无限循环、尾部段落重复），
// 检测到后立即中止模型调用，避免浪费 token。
//
// 使用滑动窗口算法：当最近 windowSize 个字符连续重复 threshold 次时触发中止。
//
// 用法：
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithHooks(repetition.New(
//	        repetition.WithWindowSize(200),  // 窗口大小（默认 200 字符）
//	        repetition.WithThreshold(3),     // 重复次数阈值（默认 3）
//	    )),
//	)
package repetition

import (
	"fmt"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// ── 配置 ──────────────────────────────────────────────────────

type Option func(*RepetitionHook)

// WithWindowSize 设置检测窗口大小（字符数）。默认 200。
// 窗口越大，检测越宽松（需要更长的重复才能触发）。
func WithWindowSize(n int) Option {
	return func(h *RepetitionHook) {
		if n > 0 {
			h.windowSize = n
		}
	}
}

// WithThreshold 设置连续重复次数阈值。默认 3。
// 例如 threshold=3 表示同一段文本连续出现 3 次才触发。
func WithThreshold(n int) Option {
	return func(h *RepetitionHook) {
		if n > 0 {
			h.threshold = n
		}
	}
}

// ── Hook 实现 ─────────────────────────────────────────────────

const storeKey = "__repetition_state"

// RepetitionHook 实现 MessageDeltaHook，在流式输出中检测重复模式。
type RepetitionHook struct {
	windowSize int
	threshold  int
}

// New 创建 RepetitionHook。
func New(opts ...Option) *RepetitionHook {
	h := &RepetitionHook{
		windowSize: 200,
		threshold:  3,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *RepetitionHook) Name() string { return "repetition" }

// repetitionState 每轮迭代的累积状态，存储在 Context store 中。
type repetitionState struct {
	iteration    int
	contentBuf   []rune
	reasoningBuf []rune
}

func (h *RepetitionHook) OnMessageDelta(c *ga.Context, delta *schema.MessageDelta) {
	state := h.getState(c)

	// 新迭代 → 重置缓冲区
	if state.iteration != c.Iteration {
		state.contentBuf = state.contentBuf[:0]
		state.reasoningBuf = state.reasoningBuf[:0]
		state.iteration = c.Iteration
	}

	// 累积并检测推理内容
	if delta.ReasoningContent != "" {
		state.reasoningBuf = append(state.reasoningBuf, []rune(delta.ReasoningContent)...)
		h.trimBuffer(&state.reasoningBuf)
		if pattern := h.detect(state.reasoningBuf); pattern != "" {
			c.AbortWithError(fmt.Errorf(
				"repetition detected in reasoning: pattern %q repeated %d times (buffer=%d chars)",
				pattern, h.threshold, len(state.reasoningBuf),
			))
			return
		}
	}

	// 累积并检测正文内容
	if delta.Content != "" {
		state.contentBuf = append(state.contentBuf, []rune(delta.Content)...)
		h.trimBuffer(&state.contentBuf)
		if pattern := h.detect(state.contentBuf); pattern != "" {
			c.AbortWithError(fmt.Errorf(
				"repetition detected in content: pattern %q repeated %d times (buffer=%d chars)",
				pattern, h.threshold, len(state.contentBuf),
			))
			return
		}
	}
}

// getState 从 Context store 获取或初始化状态。
func (h *RepetitionHook) getState(c *ga.Context) *repetitionState {
	if v, ok := c.Get(storeKey); ok {
		return v.(*repetitionState)
	}
	state := &repetitionState{
		iteration: -1,
	}
	c.Set(storeKey, state)
	return state
}

// trimBuffer 将缓冲区裁剪到最大长度（windowSize * threshold * 2），
// 保留尾部数据以维持检测窗口。
func (h *RepetitionHook) trimBuffer(buf *[]rune) {
	maxLen := h.windowSize * h.threshold * 2
	if len(*buf) > maxLen {
		tail := make([]rune, maxLen)
		copy(tail, (*buf)[len(*buf)-maxLen:])
		*buf = tail
	}
}

// detect 检测缓冲区尾部是否存在连续重复模式。
// 返回重复的模式文本（截断展示），无重复返回空串。
func (h *RepetitionHook) detect(buf []rune) string {
	minLen := h.windowSize * h.threshold
	if len(buf) < minLen {
		return ""
	}

	// 取最后一个窗口作为模式
	pattern := buf[len(buf)-h.windowSize:]

	// 向前检查 (threshold-1) 个窗口是否完全匹配
	for i := 1; i < h.threshold; i++ {
		start := len(buf) - h.windowSize*(i+1)
		chunk := buf[start : start+h.windowSize]
		if !runesEqual(chunk, pattern) {
			return ""
		}
	}

	// 截断展示
	display := string(pattern)
	if len(display) > 80 {
		display = display[:80] + "..."
	}
	return display
}

// runesEqual 比较两个 rune 切片是否相等。
func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── 断言编译期接口实现 ─────────────────────────────────────────

var _ ga.Hook = (*RepetitionHook)(nil)
var _ ga.MessageDeltaHook = (*RepetitionHook)(nil)
