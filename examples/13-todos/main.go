// Todos 任务规划 —— 展示 todo_list 工具的规划追踪能力。
//
// 展示：
//   - todos.Hook 注入 todo_list 工具（AgentStart 时自动注册）
//   - 模型通过 todo_list 工具维护结构化任务清单
//   - 每次迭代前自动注入 <todos> transient 消息
//   - TodosUpdatedEvent 自定义事件监听
//
// 运行：
//
//	go run ./examples/13-todos
package main

import (
	"context"
	"fmt"
	"log"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/ext/hook/filesystem"
	"github.com/czasg/go-agent/ext/hook/todos"
	"github.com/czasg/go-agent/harness/console"
	"github.com/czasg/go-agent/config"
)

func main() {
	cfg := config.GetConfig()
	chatModel, err := ga.NewChatModel(&cfg.LLM)
	if err != nil {
		panic(err)
	}

	agent := ga.NewAgent(
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt("你是一位编程架构设计师。"),
		ga.WithHooks(
			todos.New(),
			filesystem.New(),
		),
	)

	renderer := &todosRenderer{
		inner: console.NewTerminalRenderer(),
	}

	app := console.New(
		console.WithAgent(agent),
		console.WithRenderer(renderer),
	)

	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// todosRenderer 包装 TerminalRenderer，额外处理 todos_updated 自定义事件。
type todosRenderer struct {
	inner *console.TerminalRenderer
}

func (r *todosRenderer) OnEvent(e ga.Event) {
	if e.Type == ga.EventType("todos_updated") {
		updated, ok := e.Data.([]todos.Todo)
		if !ok {
			return
		}
		fmt.Printf("\n\033[35m📝 任务清单已更新:\033[0m\n")
		for _, t := range updated {
			marker := "  "
			switch t.Status {
			case "in_progress":
				marker = "▶ "
			case "completed":
				marker = "✓ "
			}
			fmt.Printf("  \033[35m%s%s\033[0m\n", marker, t.Content)
		}
		return
	}
	r.inner.OnEvent(e)
}

func (r *todosRenderer) OnResult(res *ga.RunResult) { r.inner.OnResult(res) }
func (r *todosRenderer) OnError(err error)          { r.inner.OnError(err) }
