// Task 克隆模式 —— 主智能体克隆自身执行子任务。
//
// 与 10-sub-agent 对比：
//   - sub-agent：注册多个专属子智能体，主智能体选择委派
//   - task（本例）：不注册子智能体，task 工具克隆当前智能体跑子任务
//
// 子任务在隔离上下文中独立运行（多步工具调用），
// 只把最终结果带回主会话，中间过程不回流。
//
// 运行：
//
//	go run ./examples/11-task
package main

import (
	"context"
	"github.com/czasg/go-agent/ext/hook/filesystem"
	"log"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/ext/tool/task"
	"github.com/czasg/go-agent/harness/console"
	"github.com/czasg/go-agent/config"
)

func main() {
	cfg := config.GetConfig()
	chatModel, err := ga.NewChatModel(&cfg.LLM)
	if err != nil {
		panic(err)
	}

	// task 工具不传 WithAgent → 克隆当前智能体。
	taskTool, err := task.New()
	if err != nil {
		panic(err)
	}

	agent := ga.NewAgent(
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt(
			"你是一个研究助手。复杂问题可以用 task 工具拆解为子任务：\n"+
				"- 每个子任务自包含，独立运行\n"+
				"- 子任务完成后把结果带回来整合\n"+
				"- 简单问题直接回答，不需要 task",
		),
		ga.WithTools(taskTool),
		ga.WithHooks(
			filesystem.New(),
		),
	)

	app := console.New(console.WithAgent(agent))

	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
