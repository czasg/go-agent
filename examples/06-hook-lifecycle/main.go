// Hook 生命周期 —— 展示五种 Hook 的触发时机与用法。
//
// 展示：
//   - AgentStartHook / AgentEndHook：Run 首尾各一次
//   - IterationStartHook / IterationEndHook：每轮迭代前后
//   - MessageEndHook：模型返回后、工具执行前
//   - c.Abort() 主动终止 loop
//
// 本示例用一个自定义 hook 在控制台打印每个生命周期点，
// 让你直观看到 hook 的触发顺序。
//
// 运行：
//
//	go run ./examples/06-hook-lifecycle
package main

import (
	"context"
	"fmt"
	"log"

	ga "github.com/czasg/go-agent"
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
		ga.WithSystemPrompt("You are a helpful assistant."),
		ga.WithHooks(&lifecycleLogger{}),
	)

	app := console.New(console.WithAgent(agent))
	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// ── lifecycleLogger：实现全部五种 Hook 接口 ────────────────────

type lifecycleLogger struct{}

func (*lifecycleLogger) Name() string { return "lifecycle-logger" }

// OnAgentStart 在 Run 开始时触发（进入循环之前）。
func (*lifecycleLogger) OnAgentStart(c *ga.Context) {
	fmt.Printf("\033[35m[hook] AgentStart — Run 开始\033[0m\n")
}

// OnIterationStart 在每轮迭代（调用模型）之前触发。
func (*lifecycleLogger) OnIterationStart(c *ga.Context) {
	fmt.Printf("\033[35m[hook] IterationStart — 迭代 #%d 开始\033[0m\n", c.Iteration)
}

// OnMessageEnd 在模型返回完整响应后触发。
// 可读取 c.Message 获取助手回复。
func (*lifecycleLogger) OnMessageEnd(c *ga.Context) {
	msg := c.Message
	if msg == nil {
		return
	}
	toolCalls := len(msg.ToolCalls)
	fmt.Printf("\033[35m[hook] MessageEnd — content=%d chars, tool_calls=%d\033[0m\n",
		len(msg.Content), toolCalls)
}

// OnIterationEnd 在每轮迭代结束后触发（工具执行完毕）。
func (*lifecycleLogger) OnIterationEnd(c *ga.Context) {
	fmt.Printf("\033[35m[hook] IterationEnd — 迭代 #%d 结束, 共 %d 条消息\033[0m\n",
		c.Iteration, len(c.Messages.Raw()))
}

// OnAgentEnd 在 Run 结束时触发（无论成败，defer 语义）。
func (*lifecycleLogger) OnAgentEnd(c *ga.Context) {
	fmt.Printf("\033[35m[hook] AgentEnd — Run 结束\033[0m\n")
}
