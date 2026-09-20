// Agent + Session 多轮对话 —— 展示框架核心能力。
//
// 与 01-llm-chat 对比：
//   - 消息历史由 Session 自动管理，无需手动拼装
//   - Agent 封装 system prompt、工具注册、生命周期 hook
//   - OnEvent 观察回调驱动流式渲染
//
// 本示例使用 harness/console 提供完整的终端交互能力：
//   - 流式输出（思考链 + 正文）
//   - 每轮统计（tokens / 耗时）
//   - 内置命令（/help /messages /stats /clear /exit）
//   - Ctrl+C 打断本轮 / 退出
//
// 运行：
//
//	go run ./examples/02-agent-chat
package main

import (
	"context"
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
	)

	app := console.New(console.WithAgent(agent))

	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
