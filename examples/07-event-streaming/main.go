// Event 事件流 —— 展示 OnEvent 与 EventHandler 的两种用法。
//
// 展示：
//   - OnEvent 手写 switch（底层，完全控制）
//   - EventHandler 便捷适配层（按需设置 Handler 字段）
//   - OnEventChannel 将事件推入 channel，适合异步消费
//
// 本示例用 EventHandler 展示每轮迭代的 token 用量和耗时统计。
//
// 运行：
//
//	go run ./examples/07-event-streaming
package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/czasg/go-agent/config"
	"os"
	"strings"
	"time"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

func main() {
	cfg := config.GetConfig()
	chatModel, err := ga.NewChatModel(&cfg.LLM)
	if err != nil {
		panic(err)
	}

	// 用 EventHandler 构建统计回调：只关心 MessageEnd 和 AgentEnd。
	handler := ga.EventHandler{
		OnMessageEndHandler: func(msg *schema.Message) {
			if msg == nil {
				return
			}
			fmt.Printf("\n\033[90m📊 tokens=%d, duration=%dms\033[0m",
				msg.Usage.TotalTokens, msg.Timing.TotalDuration)
		},
		OnAgentEndHandler: func(reason schema.StopReason) {
			fmt.Printf("\n\033[90m🏁 Run 结束, reason=%s\033[0m\n", reason)
		},
	}

	// 同时用 OnEvent 做流式输出（两种方式可以混用）。
	streamEvent := streamRenderer()

	agent := ga.NewAgent(
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt("You are a helpful assistant."),
		ga.WithOnEvent(func(e ga.Event) {
			streamEvent(e)
			handler.OnEvent()(e)
		}),
	)

	session := agent.NewSession()

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Event streaming demo. Type 'exit' to quit.")

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "exit") {
			fmt.Println("Bye!")
			break
		}

		session.AddUserMessage(input)
		if _, err := session.Run(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "\n\033[31mError: %v\033[0m\n", err)
		}
	}
}

// streamRenderer 用 OnEvent 做流式输出（与 EventHandler 混用演示）。
func streamRenderer() ga.OnEvent {
	var contentStarted bool
	var startTime time.Time

	return func(e ga.Event) {
		switch e.Type {
		case ga.IterationStartEvent:
			contentStarted = false
			startTime = time.Now()

		case ga.MessageDeltaEvent:
			delta, ok := e.Data.(*schema.MessageDelta)
			if !ok || delta == nil {
				return
			}
			if !contentStarted {
				contentStarted = true
				fmt.Printf("\n\033[36mAssistant:\033[0m ")
			}
			if delta.Content != "" {
				fmt.Print(delta.Content)
			}

		case ga.MessageEndEvent:
			if startTime.IsZero() {
				return
			}
			_ = time.Since(startTime) // 耗时已在 handler 中展示
		}
	}
}
