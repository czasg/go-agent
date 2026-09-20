// Ask —— 展示 ask 工具：模型主动向用户提问。
//
// 展示：
//   - ask.New：阻塞式 human-in-the-loop，模型向用户提问并等待回答
//   - Console 同时满足 ask.Prompter 和 REPL 的 stdin，不会抢占输入
//
// 运行：
//
//	go run ./examples/08-ask
package main

import (
	"context"
	"log"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/ext/tool/ask"
	"github.com/czasg/go-agent/harness/console"
	"github.com/czasg/go-agent/config"
)

func main() {
	cfg := config.GetConfig()
	chatModel, err := ga.NewChatModel(&cfg.LLM)
	if err != nil {
		panic(err)
	}

	c := console.NewConsole()

	askTool, err := ask.New(c)
	if err != nil {
		panic(err)
	}

	agent := ga.NewAgent(
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt(
			"你是一个助手。\n"+
				"- 如果需要向用户确认信息或征求意见，使用 ask_user 工具提问\n"+
				"- 根据用户的回答继续完成任务",
		),
		ga.WithTools(askTool),
	)

	app := console.New(
		console.WithAgent(agent),
		console.WithConsole(c),
	)

	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
