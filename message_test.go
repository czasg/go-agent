package ga

import (
	"testing"

	"github.com/czasg/go-agent/schema"
)

func msg(role schema.RoleType, content string) *schema.Message {
	return &schema.Message{Role: role, Content: content}
}

func summaryMsg(content string) *schema.Message {
	return &schema.Message{Role: schema.User, Content: content, Summary: true}
}

func assistantWithUsage(content string, total int) *schema.Message {
	return &schema.Message{
		Role:    schema.Assistant,
		Content: content,
		Usage:   schema.TokenUsage{TotalTokens: total},
	}
}

// ── Messages() ────────────────────────────────────────────────

func TestMessages_NoSummary(t *testing.T) {
	s := NewMessageStore(msg(schema.System, "sys"), msg(schema.User, "u1"), msg(schema.Assistant, "a1"))
	msgs := s.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3, got %d", len(msgs))
	}
}

func TestMessages_WithSummary_NoSystem(t *testing.T) {
	s := NewMessageStore(
		summaryMsg("summary1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
	)
	msgs := s.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3, got %d", len(msgs))
	}
	if msgs[0].Content != "summary1" {
		t.Fatalf("expected summary1, got %s", msgs[0].Content)
	}
}

func TestMessages_WithSummary_AndSystem(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		summaryMsg("summary1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
	)
	msgs := s.Messages()
	// 应该是 [system, summary, u2, a2]
	if len(msgs) != 4 {
		t.Fatalf("expected 4, got %d", len(msgs))
	}
	if msgs[0].Role != schema.System {
		t.Fatalf("expected system at 0, got %s", msgs[0].Role)
	}
	if msgs[0].Content != "sys" {
		t.Fatalf("expected sys, got %s", msgs[0].Content)
	}
	if msgs[1].Content != "summary1" {
		t.Fatalf("expected summary1, got %s", msgs[1].Content)
	}
	if msgs[2].Content != "u2" {
		t.Fatalf("expected u2, got %s", msgs[2].Content)
	}
}

func TestMessages_MultipleSummaries(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		summaryMsg("s1"),
		msg(schema.User, "u2"),
		summaryMsg("s2"),
		msg(schema.User, "u3"),
		msg(schema.Assistant, "a3"),
	)
	msgs := s.Messages()
	// 应该是 [system, s2, u3, a3]（从最后一条 summary 开始）
	if len(msgs) != 4 {
		t.Fatalf("expected 4, got %d", len(msgs))
	}
	if msgs[0].Role != schema.System {
		t.Fatalf("expected system at 0, got %s", msgs[0].Role)
	}
	if msgs[1].Content != "s2" {
		t.Fatalf("expected s2, got %s", msgs[1].Content)
	}
}

// ── Raw() ─────────────────────────────────────────────────────

func TestRaw(t *testing.T) {
	s := NewMessageStore(msg(schema.System, "sys"), summaryMsg("s1"), msg(schema.User, "u1"))
	raw := s.Raw()
	if len(raw) != 3 {
		t.Fatalf("expected 3, got %d", len(raw))
	}
}

// ── Insert ────────────────────────────────────────────────────

func TestInsert_NoSummary(t *testing.T) {
	s := NewMessageStore(msg(schema.User, "u1"), msg(schema.Assistant, "a1"))
	s.Insert(1, msg(schema.Tool, "t1"))
	msgs := s.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3, got %d", len(msgs))
	}
	if msgs[1].Role != schema.Tool {
		t.Fatalf("expected tool at 1, got %s", msgs[1].Role)
	}
}

func TestInsert_WithSummary(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		summaryMsg("s1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
	)
	// Messages() = [system, s1, u2, a2]
	// Insert at 2（u2 前面）
	s.Insert(2, msg(schema.Tool, "t1"))
	msgs := s.Messages()
	if len(msgs) != 5 {
		t.Fatalf("expected 5, got %d", len(msgs))
	}
	if msgs[2].Role != schema.Tool {
		t.Fatalf("expected tool at 2, got %s", msgs[2].Role)
	}
	// system 应该还在最前面
	if msgs[0].Role != schema.System {
		t.Fatalf("expected system at 0, got %s", msgs[0].Role)
	}
}

func TestInsert_AppendToEnd(t *testing.T) {
	s := NewMessageStore(msg(schema.User, "u1"))
	s.Insert(1, msg(schema.Assistant, "a1"))
	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2, got %d", len(msgs))
	}
	if msgs[1].Content != "a1" {
		t.Fatalf("expected a1, got %s", msgs[1].Content)
	}
}

// ── Delete ────────────────────────────────────────────────────

func TestDelete_NoSummary(t *testing.T) {
	s := NewMessageStore(
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
	)
	s.Delete(0, 2)
	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2, got %d", len(msgs))
	}
	if msgs[0].Content != "u2" {
		t.Fatalf("expected u2, got %s", msgs[0].Content)
	}
}

func TestDelete_WithSummary(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		summaryMsg("s1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
		msg(schema.User, "u3"),
		msg(schema.Assistant, "a3"),
	)
	// Messages() = [system, s1, u2, a2, u3, a3]
	// Delete(2, 4) 删除 u2, a2
	s.Delete(2, 4)
	msgs := s.Messages()
	if len(msgs) != 4 {
		t.Fatalf("expected 4, got %d", len(msgs))
	}
	if msgs[0].Role != schema.System {
		t.Fatalf("expected system at 0, got %s", msgs[0].Role)
	}
	if msgs[1].Content != "s1" {
		t.Fatalf("expected s1, got %s", msgs[1].Content)
	}
	if msgs[2].Content != "u3" {
		t.Fatalf("expected u3, got %s", msgs[2].Content)
	}
}

// ── Rounds ────────────────────────────────────────────────────

func TestRounds_Basic(t *testing.T) {
	s := NewMessageStore(
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		msg(schema.Tool, "t1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
	)
	rounds := s.Rounds()
	if len(rounds) != 2 {
		t.Fatalf("expected 2 rounds, got %d", len(rounds))
	}
	if rounds[0].Messages[0].Content != "u1" {
		t.Fatalf("expected u1, got %s", rounds[0].Messages[0].Content)
	}
	if len(rounds[0].Messages) != 3 { // u1, a1, t1
		t.Fatalf("expected 3 msgs in round 0, got %d", len(rounds[0].Messages))
	}
	if rounds[1].Messages[0].Content != "u2" {
		t.Fatalf("expected u2, got %s", rounds[1].Messages[0].Content)
	}
}

func TestRounds_SkipsSystemAndSummary(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		summaryMsg("s1"),
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
	)
	rounds := s.Rounds()
	if len(rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(rounds))
	}
	if rounds[0].Messages[0].Content != "u1" {
		t.Fatalf("expected u1, got %s", rounds[0].Messages[0].Content)
	}
}

// ── ExtractBeforeRounds ───────────────────────────────────────

func TestExtractBeforeRounds_Basic(t *testing.T) {
	s := NewMessageStore(
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
		msg(schema.User, "u3"),
		msg(schema.Assistant, "a3"),
		msg(schema.User, "u4"),
		msg(schema.Assistant, "a4"),
	)
	// 保留最后2轮，提取前面的
	extracted := s.ExtractBeforeRounds(2)
	if extracted == nil {
		t.Fatal("expected extraction, got nil")
	}
	if extracted.Start != 0 {
		t.Fatalf("expected start 0, got %d", extracted.Start)
	}
	if extracted.End != 4 {
		t.Fatalf("expected end 4, got %d", extracted.End)
	}
	if len(extracted.Messages) != 4 {
		t.Fatalf("expected 4 msgs, got %d", len(extracted.Messages))
	}
}

func TestExtractBeforeRounds_NotEnoughRounds(t *testing.T) {
	s := NewMessageStore(
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
	)
	extracted := s.ExtractBeforeRounds(2)
	if extracted != nil {
		t.Fatal("expected nil, not enough rounds")
	}
}

func TestExtractBeforeRounds_SkipsSystemAndSummary(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		summaryMsg("s1"),
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
		msg(schema.User, "u3"),
		msg(schema.Assistant, "a3"),
	)
	extracted := s.ExtractBeforeRounds(2)
	if extracted == nil {
		t.Fatal("expected extraction, got nil")
	}
	// 应该跳过 system 和 summary
	if extracted.Start != 2 {
		t.Fatalf("expected start 2 (skip system+summary), got %d", extracted.Start)
	}
}

// ── ExtractBeforeRounds + Delete + Insert 完整流程 ────────────

func TestExtractAndReplace(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
		msg(schema.User, "u3"),
		msg(schema.Assistant, "a3"),
		msg(schema.User, "u4"),
		msg(schema.Assistant, "a4"),
	)

	extracted := s.ExtractBeforeRounds(2)
	if extracted == nil {
		t.Fatal("expected extraction")
	}

	// 删除旧消息，插入 summary
	ck := summaryMsg("summary of u1-u2")
	s.Delete(extracted.Start, extracted.End)
	s.Insert(extracted.Start, ck)

	msgs := s.Messages()
	// 应该是 [system, summary, u3, a3, u4, a4]
	if len(msgs) != 6 {
		t.Fatalf("expected 6, got %d", len(msgs))
	}
	if msgs[0].Role != schema.System {
		t.Fatalf("expected system at 0, got %s", msgs[0].Role)
	}
	if msgs[1].Content != "summary of u1-u2" {
		t.Fatalf("expected summary, got %s", msgs[1].Content)
	}
	if !msgs[1].Summary {
		t.Fatal("expected summary flag")
	}
	if msgs[2].Content != "u3" {
		t.Fatalf("expected u3, got %s", msgs[2].Content)
	}
}

// ── LastMessageByRole ─────────────────────────────────────────

func TestLastMessageByRole_Found(t *testing.T) {
	s := NewMessageStore(
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
	)
	idx, m := s.LastAssistantMessage()
	if idx != 3 {
		t.Fatalf("expected idx 3, got %d", idx)
	}
	if m.Content != "a2" {
		t.Fatalf("expected a2, got %s", m.Content)
	}
}

func TestLastMessageByRole_NotFound(t *testing.T) {
	s := NewMessageStore(msg(schema.User, "u1"))
	idx, m := s.LastAssistantMessage()
	if idx != -1 {
		t.Fatalf("expected -1, got %d", idx)
	}
	if m != nil {
		t.Fatal("expected nil")
	}
}

func TestLastMessageByRole_WithSummary(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		summaryMsg("s1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
	)
	// Messages() = [system, s1, u2, a2]
	idx, m := s.LastAssistantMessage()
	if idx != 3 {
		t.Fatalf("expected idx 3, got %d", idx)
	}
	if m.Content != "a2" {
		t.Fatalf("expected a2, got %s", m.Content)
	}
}

// ── TotalTokens ───────────────────────────────────────────────

func TestTotalTokens_WithUsage(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		msg(schema.User, "u1"),
		assistantWithUsage("a1", 500),
		msg(schema.Tool, "t1"), // 之后新增的，需要补算
	)
	total := s.TotalTokens()
	// 500 (usage) + t1 估算
	expected := 500 + msg(schema.Tool, "t1").EstimateTokens()
	if total != expected {
		t.Fatalf("expected %d, got %d", expected, total)
	}
}

func TestTotalTokens_NoUsage(t *testing.T) {
	s := NewMessageStore(
		msg(schema.System, "sys"),
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
	)
	total := s.TotalTokens()
	// 全部估算
	expected := msg(schema.System, "sys").EstimateTokens() +
		msg(schema.User, "u1").EstimateTokens() +
		msg(schema.Assistant, "a1").EstimateTokens()
	if total != expected {
		t.Fatalf("expected %d, got %d", expected, total)
	}
}

// ── RoundCount ────────────────────────────────────────────────

func TestRoundCount(t *testing.T) {
	s := NewMessageStore(
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
		msg(schema.User, "u3"),
		msg(schema.Assistant, "a3"),
	)
	if s.RoundCount() != 3 {
		t.Fatalf("expected 3, got %d", s.RoundCount())
	}
}

// ── AddMessage / AddUserMessage ───────────────────────────────

func TestAddMessage(t *testing.T) {
	s := NewMessageStore()
	s.AddMessage(msg(schema.User, "u1"), msg(schema.Assistant, "a1"))
	if len(s.Messages()) != 2 {
		t.Fatalf("expected 2, got %d", len(s.Messages()))
	}
}

func TestAddUserMessage(t *testing.T) {
	s := NewMessageStore()
	s.AddUserMessage("hello")
	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1, got %d", len(msgs))
	}
	if msgs[0].Role != schema.User || msgs[0].Content != "hello" {
		t.Fatalf("unexpected message: %s %s", msgs[0].Role, msgs[0].Content)
	}
}

// ── Delete + Insert 不破坏原数组 ──────────────────────────────

func TestInsertDelete_NoCorruption(t *testing.T) {
	s := NewMessageStore(
		msg(schema.User, "u1"),
		msg(schema.Assistant, "a1"),
		msg(schema.User, "u2"),
		msg(schema.Assistant, "a2"),
		msg(schema.User, "u3"),
		msg(schema.Assistant, "a3"),
	)

	s.Delete(0, 2)
	s.Insert(0, summaryMsg("s1"))

	msgs := s.Messages()
	if len(msgs) != 5 {
		t.Fatalf("expected 5, got %d", len(msgs))
	}
	// 确认没有数据损坏
	if msgs[0].Content != "s1" {
		t.Fatalf("expected s1, got %s", msgs[0].Content)
	}
	if msgs[1].Content != "u2" {
		t.Fatalf("expected u2, got %s", msgs[1].Content)
	}
	if msgs[2].Content != "a2" {
		t.Fatalf("expected a2, got %s", msgs[2].Content)
	}
}
