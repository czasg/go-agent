// Filesystem 内置文件工具 —— 展示 Hook 形态注入工具。
//
// 展示：
//   - filesystem.New() 作为 AgentStartHook，在 Run 开始时自动注入工具
//   - 四个内置工具：read_file、write_file、edit_file、terminal
//   - Hook 与 Agent 的集成方式
//
// 运行：
//
//	go run ./examples/05-filesystem
package main

import (
	"context"
	"log"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/ext/hook/filesystem"
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
		ga.WithSystemPrompt("你是一个编程助手，可以读写文件、执行终端命令。修改文件前请先阅读确认内容。"),
		ga.WithHooks(filesystem.New(filesystem.WithDir(config.Dir))),
	)

	app := console.New(console.WithAgent(agent))
	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
