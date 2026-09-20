// Skill 技能系统 —— 展示 skill hook 的按需加载能力。
//
// 展示：
//   - skill.Hook 注入 skill 工具到 Agent
//   - 模型通过 skill 工具按需加载技能指令
//   - FilesystemBackend 扫描 SKILL.md 文件
//
// 运行：
//
//	go run ./examples/12-skill
package main

import (
	"context"
	"log"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/ext/hook/filesystem"
	"github.com/czasg/go-agent/ext/hook/skill"
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
		ga.WithHooks(
			filesystem.New(),
			skill.New(skill.WithDir(config.Dir)),
		),
	)

	app := console.New(console.WithAgent(agent))

	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
