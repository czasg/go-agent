// Package todos 提供一个规划追踪 Hook：在 Agent 启动时注册 todo_list 工具并注入规划提示词，
// 每次迭代前自动将当前任务清单注入为 transient 消息。
//
// 用法：
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithHooks(todos.New()),
//	)
//
// 自定义提示词：
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithHooks(todos.New(todos.WithPrompt("你的自定义提示词..."))),
//	)
package todos

import (
	"encoding/json"
	"fmt"
	"strings"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// Option 配置 Hook 的函数选项。
type Option func(*Hook)

// WithPrompt 覆盖默认的规划提示词。
func WithPrompt(prompt string) Option {
	return func(h *Hook) {
		h.prompt = prompt
	}
}

// TodosUpdatedEvent 自定义事件，每次 todo_list 工具更新清单时触发。
const TodosUpdatedEvent ga.EventType = "todos_updated"

// New 创建 todos Hook。
func New(opts ...Option) *Hook {
	h := &Hook{
		tool: &todoTool{},
	}
	for _, opt := range opts {
		opt(h)
	}
	if h.prompt == "" {
		h.prompt = defaultPrompt
	}
	return h
}

// ── 类型定义 ──────────────────────────────────────────

// 单条待办。
type Todo struct {
	Content    string `json:"content"    description:"待办内容，祈使句形式，如 '修复登录 bug'"`
	ActiveForm string `json:"activeForm" description:"进行中的现在进行时形式，如 '正在修复登录 bug'"`
	Status     string `json:"status"     description:"任务状态" enum:"pending,in_progress,completed"`
}

// ── Hook ──────────────────────────────────────────────

// Hook 实现 AgentStartHook + IterationStartHook。
// AgentStart 注册 todo_list 工具并注入规划提示词；IterationStart 注入 transient 消息。
type Hook struct {
	tool   *todoTool
	prompt string
}

func (h *Hook) Name() string { return "todos" }

// OnAgentStart 注册 todo_list 工具并注入规划提示词。
func (h *Hook) OnAgentStart(c *ga.Context) {
	c.Tools.Register(h.tool)
	c.Messages.AppendSystemHint(h.prompt)
}

// OnIterationStart 在每次迭代前注入当前任务清单（如果存在）。
// Transient=true 保证下轮 ClearTransient 自动清除。
func (h *Hook) OnIterationStart(c *ga.Context) {
	if len(h.tool.todos) == 0 {
		return
	}
	c.Messages.AddMessage(&schema.Message{
		Role:      schema.User,
		Content:   formatTodos(h.tool.todos),
		Transient: true,
	})
}

// ── Tool ──────────────────────────────────────────────

var toolInfo = schema.ToolInfo{
	Name: "todo_list",
	Description: "创建和管理当前会话的结构化任务清单，用于复杂多步骤任务的规划与进度追踪。" +
		"每次调用传入完整列表（全量覆盖）。" +
		"当任务需要 3 步以上、或用户给出多个需求时主动使用。" +
		"简单任务（<3 步）直接执行，无需使用此工具。",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos": map[string]any{
				"type":        "array",
				"description": "完整的待办列表，每次调用传全量覆盖",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content":    map[string]any{"type": "string", "description": "待办内容，祈使句形式"},
						"activeForm": map[string]any{"type": "string", "description": "进行中的现在进行时形式"},
						"status":     map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}, "description": "任务状态"},
					},
					"required": []string{"content", "status"},
				},
			},
		},
		"required": []string{"todos"},
	},
}

type todoTool struct {
	todos []Todo
}

func (t *todoTool) Info() schema.ToolInfo { return toolInfo }

func (t *todoTool) Execute(c *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct {
		Todos []Todo `json:"todos"`
	}
	if err := call.JSON(&in); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	t.todos = in.Todos

	// 自我检测：如果所有任务都已完成，清空列表
	if allCompleted(t.todos) {
		t.todos = nil
	} else {
		c.Message.Transient = true // 如果未完成，则标记当前消息自动清除，最新计划由 OnIterationStart 动态注入
	}

	c.Emit(TodosUpdatedEvent, t.todos)

	data, _ := json.Marshal(t.todos)
	return fmt.Sprintf("已更新待办列表: %s", string(data)), nil
}

// ── 辅助 ──────────────────────────────────────────────

// allCompleted 检查是否所有任务都已完成。
func allCompleted(todos []Todo) bool {
	if len(todos) == 0 {
		return false
	}
	for _, t := range todos {
		if t.Status != "completed" {
			return false
		}
	}
	return true
}

func formatTodos(todos []Todo) string {
	var b strings.Builder
	b.WriteString("<todos>\n")
	for _, t := range todos {
		switch t.Status {
		case "in_progress":
			b.WriteString("▶ ")
		case "completed":
			b.WriteString("✓ ")
		default:
			b.WriteString("  ")
		}
		b.WriteString(t.Content)
		if t.ActiveForm != "" && t.Status == "in_progress" {
			b.WriteString(" (" + t.ActiveForm + ")")
		}
		b.WriteString("\n")
	}
	b.WriteString("</todos>")
	return b.String()
}

const defaultPrompt = `# 任务规划（todo_list）

你拥有一个 todo_list 工具，用于在复杂多步骤任务中维护结构化任务清单，帮助你和用户追踪进度。

## 何时使用
1. 任务需要 3 步以上时，主动使用
2. 用户给出多个需求时，主动使用
3. 用户明确要求规划时使用

## 何时不使用
- 只有单个简单任务（<3 步）时，直接执行
- 纯对话或信息查询时，不使用

## 任务状态
- pending：待处理
- in_progress：当前正在做（同一时间只能有 ONE 个任务处于此状态）
- completed：已完成
`
