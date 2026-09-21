package todos

import (
	"testing"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

func TestOnIterationStart_InjectsTransient(t *testing.T) {
	// 验证 OnIterationStart 在有 todos 时注入 transient 消息。
	store := ga.NewMessageStore(
		&schema.Message{Role: schema.System, Content: "sys"},
		&schema.Message{Role: schema.User, Content: "hello"},
	)
	h := New()
	h.tool.todos = []Todo{{Content: "task1", Status: "pending"}}
	c := &ga.Context{Messages: store}

	h.OnIterationStart(c)

	msgs := store.Messages()
	// 应有: system, user, user(transient)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if !msgs[2].Transient {
		t.Fatal("expected transient message")
	}
	if msgs[2].Role != schema.User {
		t.Fatalf("expected user, got %s", msgs[2].Role)
	}
}

func TestOnIterationStart_NoTodosNoInjection(t *testing.T) {
	// 没有 todos 时不应注入任何消息。
	store := ga.NewMessageStore(
		&schema.Message{Role: schema.System, Content: "sys"},
		&schema.Message{Role: schema.User, Content: "hello"},
	)
	h := New()
	c := &ga.Context{Messages: store}

	h.OnIterationStart(c)

	msgs := store.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}
