package repetition

import (
	"context"
	"strings"
	"testing"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// helper：构造一个最小可用的 Context。
func newTestContext() *ga.Context {
	agent := ga.NewAgent()
	return ga.NewContext(context.Background(), agent, nil)
}

// helper：构造 Content delta。
func contentDelta(text string) *schema.MessageDelta {
	return &schema.MessageDelta{Content: text}
}

// helper：构造 ReasoningContent delta。
func reasoningDelta(text string) *schema.MessageDelta {
	return &schema.MessageDelta{ReasoningContent: text}
}

// ── 基本检测 ───────────────────────────────────────────────────

func TestDetect_ConsecutiveRepetition_Triggers(t *testing.T) {
	// windowSize=10, threshold=3 → 同一段 10 字符连续出现 3 次即触发
	h := New(WithWindowSize(10), WithThreshold(3))
	c := newTestContext()
	pattern := "abcdefghij" // 10 chars

	// 发送 3 轮相同的 10 字符
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta(pattern))

	aborted, err := c.StopState()
	if !aborted {
		t.Fatal("expected abort on 3 consecutive repetitions")
	}
	if err == nil || !strings.Contains(err.Error(), "repetition detected in content") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDetect_ConsecutiveReasoning_Triggers(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(3))
	c := newTestContext()
	pattern := "1234567890"

	h.OnMessageDelta(c, reasoningDelta(pattern))
	h.OnMessageDelta(c, reasoningDelta(pattern))
	h.OnMessageDelta(c, reasoningDelta(pattern))

	aborted, err := c.StopState()
	if !aborted {
		t.Fatal("expected abort on reasoning repetition")
	}
	if err == nil || !strings.Contains(err.Error(), "repetition detected in reasoning") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── 非连续重复不应触发 ─────────────────────────────────────────

func TestDetect_NonConsecutive_NoTrigger(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(3))
	c := newTestContext()
	pattern := "abcdefghij"
	separator := "XXXXXXXXXX" // 10 chars, 不同内容

	// pattern 出现 3 次，但中间被不同内容隔开 → 不是连续重复
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta(separator))
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta(separator))
	h.OnMessageDelta(c, contentDelta(pattern))

	aborted, _ := c.StopState()
	if aborted {
		t.Fatal("non-consecutive repetition should NOT trigger abort")
	}
}

func TestDetect_PartiallyConsecutive_NoTrigger(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(3))
	c := newTestContext()
	pattern := "abcdefghij"

	// 只连续 2 次，第 3 次被隔开
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta("ZZZZZZZZZZ"))
	h.OnMessageDelta(c, contentDelta(pattern))

	aborted, _ := c.StopState()
	if aborted {
		t.Fatal("partially consecutive repetition should NOT trigger abort")
	}
}

// ── 阈值边界 ──────────────────────────────────────────────────

func TestDetect_BelowThreshold_NoTrigger(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(3))
	c := newTestContext()
	pattern := "abcdefghij"

	// 只连续 2 次（< threshold=3）→ 不触发
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta(pattern))

	aborted, _ := c.StopState()
	if aborted {
		t.Fatal("repetition below threshold should NOT trigger")
	}
}

func TestDetect_ExactlyAtThreshold_Triggers(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(2))
	c := newTestContext()
	pattern := "abcdefghij"

	// threshold=2，连续 2 次即触发
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta(pattern))

	aborted, _ := c.StopState()
	if !aborted {
		t.Fatal("expected abort at exactly threshold")
	}
}

// ── 累积 delta（同一个 pattern 分多个 chunk 到达）─────────────

func TestDetect_AccumulatedChunks_Triggers(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(3))
	c := newTestContext()

	// 同一个 pattern 分两次到达，拼接后仍是连续重复
	h.OnMessageDelta(c, contentDelta("abcde"))
	h.OnMessageDelta(c, contentDelta("fghij")) // 累积 = "abcdefghij"
	h.OnMessageDelta(c, contentDelta("abcdefghij"))
	h.OnMessageDelta(c, contentDelta("abcdefghij"))

	aborted, _ := c.StopState()
	if !aborted {
		t.Fatal("accumulated chunks forming repetition should trigger")
	}
}

// ── 不足一个窗口 → 不触发 ─────────────────────────────────────

func TestDetect_BufferTooSmall_NoTrigger(t *testing.T) {
	h := New(WithWindowSize(100), WithThreshold(3))
	c := newTestContext()

	// 总共只有 50 字符，远不足 windowSize*threshold=300
	for i := 0; i < 5; i++ {
		h.OnMessageDelta(c, contentDelta("abcdefghij"))
	}

	aborted, _ := c.StopState()
	if aborted {
		t.Fatal("buffer smaller than windowSize*threshold should NOT trigger")
	}
}

// ── 迭代重置 ──────────────────────────────────────────────────

func TestDetect_IterationReset(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(3))
	c := newTestContext()
	pattern := "abcdefghij"

	// 第一轮迭代：累积 2 次（不足阈值）
	c.Iteration = 0
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta(pattern))

	aborted, _ := c.StopState()
	if aborted {
		t.Fatal("should not trigger in iteration 0")
	}

	// 第二轮迭代：缓冲区应已重置，再累积 2 次仍不足阈值
	c.Iteration = 1
	h.OnMessageDelta(c, contentDelta(pattern))
	h.OnMessageDelta(c, contentDelta(pattern))

	aborted, _ = c.StopState()
	if aborted {
		t.Fatal("iteration reset should clear buffer; 2 repetitions in new iteration should NOT trigger")
	}

	// 第二轮再加 1 次 → 连续 3 次 → 触发
	h.OnMessageDelta(c, contentDelta(pattern))

	aborted, _ = c.StopState()
	if !aborted {
		t.Fatal("3 consecutive repetitions within same iteration should trigger")
	}
}

// ── 缓冲区裁剪 ────────────────────────────────────────────────

func TestTrimBuffer_BoundsLength(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(3))
	// maxLen = 10 * 3 * 2 = 60
	buf := make([]rune, 0, 200)
	for i := 0; i < 200; i++ {
		buf = append(buf, 'a')
	}
	h.trimBuffer(&buf)
	if len(buf) != 60 {
		t.Fatalf("expected trimmed length 60, got %d", len(buf))
	}
}

func TestTrimBuffer_ShortBufferUnchanged(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(3))
	buf := []rune("short")
	h.trimBuffer(&buf)
	if len(buf) != 5 {
		t.Fatalf("short buffer should be unchanged, got length %d", len(buf))
	}
}

// ── detect 单元测试 ────────────────────────────────────────────

func TestDetect_ExactMatch(t *testing.T) {
	h := New(WithWindowSize(5), WithThreshold(3))
	// "abcde" × 3 = 15 chars
	buf := []rune("abcdeabcdeabcde")
	pattern := h.detect(buf)
	if pattern == "" {
		t.Fatal("expected detection of exact 3x repetition")
	}
}

func TestDetect_TwoOfThree_NoTrigger(t *testing.T) {
	h := New(WithWindowSize(5), WithThreshold(3))
	// 只有 2 次重复 + 1 次不同
	buf := []rune("abcdeabcdeXXXXX")
	pattern := h.detect(buf)
	if pattern != "" {
		t.Fatal("2-of-3 should not trigger with threshold=3")
	}
}

func TestDetect_PatternLongerThan80_Truncated(t *testing.T) {
	h := New(WithWindowSize(100), WithThreshold(2))
	long := strings.Repeat("x", 100)
	buf := []rune(long + long)
	pattern := h.detect(buf)
	if !strings.HasSuffix(pattern, "...") {
		t.Fatalf("expected truncated pattern, got %q", pattern)
	}
	if len(pattern) != 83 { // 80 + "..."
		t.Fatalf("expected 83 chars, got %d", len(pattern))
	}
}

// ── 空 delta 不影响 ───────────────────────────────────────────

func TestOnMessageDelta_EmptyDelta_NoEffect(t *testing.T) {
	h := New(WithWindowSize(10), WithThreshold(3))
	c := newTestContext()

	// 空 delta 不应产生任何副作用
	h.OnMessageDelta(c, contentDelta(""))
	h.OnMessageDelta(c, reasoningDelta(""))
	h.OnMessageDelta(c, &schema.MessageDelta{})

	aborted, _ := c.StopState()
	if aborted {
		t.Fatal("empty deltas should not trigger")
	}
}

// ── 默认配置 ──────────────────────────────────────────────────

func TestDefaultConfig(t *testing.T) {
	h := New()
	if h.windowSize != 200 {
		t.Fatalf("default windowSize should be 200, got %d", h.windowSize)
	}
	if h.threshold != 3 {
		t.Fatalf("default threshold should be 3, got %d", h.threshold)
	}
}

func TestName(t *testing.T) {
	h := New()
	if h.Name() != "repetition" {
		t.Fatalf("expected name 'repetition', got %q", h.Name())
	}
}
