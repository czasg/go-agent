package skill

import (
	"context"
	"fmt"
	"strings"
	"testing"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// InMemoryBackend 是基于内存的 Backend 实现，仅用于测试。
type InMemoryBackend struct {
	Skills []Skill
}

func (b *InMemoryBackend) List(_ context.Context) ([]FrontMatter, error) {
	out := make([]FrontMatter, 0, len(b.Skills))
	for _, s := range b.Skills {
		out = append(out, s.FrontMatter)
	}
	return out, nil
}

func (b *InMemoryBackend) Get(_ context.Context, name string) (Skill, error) {
	for _, s := range b.Skills {
		if s.Name == name {
			return s, nil
		}
	}
	return Skill{}, fmt.Errorf("skill not found: %s", name)
}

// ── New() 选项测试 ─────────────────────────────────────────────

func TestNew(t *testing.T) {
	t.Run("零选项使用默认 backend", func(t *testing.T) {
		h := New()
		if h.tool.b == nil {
			t.Fatal("backend should have default")
		}
		if _, ok := h.tool.b.(*FilesystemBackend); !ok {
			t.Fatalf("want FilesystemBackend, got %T", h.tool.b)
		}
		if !strings.Contains(h.instruction, "skill") {
			t.Fatalf("instruction should mention tool: %q", h.instruction)
		}
	})

	t.Run("WithBackend 覆盖默认", func(t *testing.T) {
		b := &InMemoryBackend{Skills: []Skill{
			{FrontMatter: FrontMatter{Name: "test", Description: "desc"}},
		}}
		h := New(WithBackend(b))
		if _, ok := h.tool.b.(*InMemoryBackend); !ok {
			t.Fatalf("want InMemoryBackend, got %T", h.tool.b)
		}
	})

	t.Run("WithDir 覆盖默认", func(t *testing.T) {
		dir := t.TempDir()
		h := New(WithDir(dir))
		if _, ok := h.tool.b.(*FilesystemBackend); !ok {
			t.Fatalf("want FilesystemBackend, got %T", h.tool.b)
		}
	})
}

// ── InMemoryBackend 测试 ──────────────────────────────────────

func TestInMemoryBackend(t *testing.T) {
	b := &InMemoryBackend{Skills: []Skill{
		{FrontMatter: FrontMatter{Name: "a", Description: "desc a"}, Content: "content a"},
		{FrontMatter: FrontMatter{Name: "b", Description: "desc b"}, Content: "content b"},
	}}

	ctx := context.Background()

	t.Run("List", func(t *testing.T) {
		matters, err := b.List(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(matters) != 2 {
			t.Fatalf("want 2, got %d", len(matters))
		}
		if matters[0].Name != "a" || matters[1].Name != "b" {
			t.Fatalf("names: %v, %v", matters[0].Name, matters[1].Name)
		}
	})

	t.Run("Get 存在的", func(t *testing.T) {
		s, err := b.Get(ctx, "a")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if s.Content != "content a" {
			t.Fatalf("content: %q", s.Content)
		}
	})

	t.Run("Get 不存在的", func(t *testing.T) {
		_, err := b.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("want error")
		}
	})
}

// ── Hook 集成测试 ─────────────────────────────────────────────

func TestHook_OnAgentStart(t *testing.T) {
	backend := &InMemoryBackend{Skills: []Skill{
		{FrontMatter: FrontMatter{Name: "pdf", Description: "PDF processing"}, Content: "full instructions"},
		{FrontMatter: FrontMatter{Name: "sql", Description: "SQL queries"}, Content: "sql guide"},
	}}

	h := New(WithBackend(backend))

	// 构造 Agent + Context
	agent := ga.NewAgent()
	c := ga.NewContext(context.Background(), agent, nil)

	// 执行 hook
	h.OnAgentStart(c)

	// 验证工具已注册
	tool, ok := c.Tools.Get("skill")
	if !ok {
		t.Fatal("skill tool should be registered")
	}

	// 验证工具 Info 包含 skill 列表
	info := tool.Info()
	if info.Name != "skill" {
		t.Fatalf("tool name: %q", info.Name)
	}
	if !strings.Contains(info.Description, "pdf") || !strings.Contains(info.Description, "PDF processing") {
		t.Fatalf("description should list skills: %q", info.Description)
	}
	if !strings.Contains(info.Description, "sql") || !strings.Contains(info.Description, "SQL queries") {
		t.Fatalf("description should list sql skill: %q", info.Description)
	}

	// 验证 system prompt 已注入
	msgs := c.Messages.Messages()
	if len(msgs) == 0 || msgs[0].Role != schema.System {
		t.Fatal("system message should be injected")
	}
	if !strings.Contains(msgs[0].Content, "Skills directory:") {
		t.Fatalf("system should contain skills directory: %q", msgs[0].Content)
	}
}

func TestHook_OnAgentStart_已有system消息(t *testing.T) {
	backend := &InMemoryBackend{Skills: []Skill{
		{FrontMatter: FrontMatter{Name: "test", Description: "test skill"}, Content: "instructions"},
	}}

	h := New(WithBackend(backend))

	agent := ga.NewAgent(ga.WithSystemPrompt("original system prompt"))
	c := ga.NewContext(context.Background(), agent, nil)

	h.OnAgentStart(c)

	msgs := c.Messages.Messages()
	if len(msgs) == 0 || msgs[0].Role != schema.System {
		t.Fatal("system message should exist")
	}
	// 应该是同一条 system 消息，拼接了 skill 指令
	if !strings.Contains(msgs[0].Content, "original system prompt") {
		t.Fatalf("should keep original: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "Skills directory:") {
		t.Fatalf("should contain skills directory: %q", msgs[0].Content)
	}
}

func TestSkillTool_Execute(t *testing.T) {
	backend := &InMemoryBackend{Skills: []Skill{
		{FrontMatter: FrontMatter{Name: "pdf", Description: "PDF processing"}, Content: "full instructions here", BaseDirectory: "/skills/pdf"},
	}}

	h := New(WithBackend(backend))

	agent := ga.NewAgent()
	c := ga.NewContext(context.Background(), agent, nil)
	h.OnAgentStart(c)

	tool, _ := c.Tools.Get("skill")

	t.Run("正常调用", func(t *testing.T) {
		call := &schema.ToolCall{
			Function: schema.FunctionCall{
				Name:      "skill",
				Arguments: `{"skill": "pdf"}`,
			},
		}
		result, err := tool.Execute(c, call)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(result, "Launching skill: pdf") {
			t.Fatalf("result: %q", result)
		}
		if !strings.Contains(result, "full instructions here") {
			t.Fatalf("result should contain content: %q", result)
		}
		if !strings.Contains(result, "/skills/pdf") {
			t.Fatalf("result should contain base dir: %q", result)
		}
	})

	t.Run("skill 不存在", func(t *testing.T) {
		call := &schema.ToolCall{
			Function: schema.FunctionCall{
				Name:      "skill",
				Arguments: `{"skill": "nonexistent"}`,
			},
		}
		_, err := tool.Execute(c, call)
		if err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("空参数", func(t *testing.T) {
		call := &schema.ToolCall{
			Function: schema.FunctionCall{
				Name:      "skill",
				Arguments: `{"skill": ""}`,
			},
		}
		_, err := tool.Execute(c, call)
		if err == nil {
			t.Fatal("want error for empty skill name")
		}
	})
}

// ── system prompt 测试 ────────────────────────────────────────

func TestWithSystemPrompt(t *testing.T) {
	h := New(WithSystemPrompt("custom prompt"))
	if h.instruction != "custom prompt" {
		t.Fatalf("instruction: %q", h.instruction)
	}
}
