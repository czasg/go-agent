// Sub-Agent 模式 —— 主智能体委派任务给专属子智能体。
//
// 展示：
//   - ga.WithName / WithDescription 定义子智能体身份
//   - task.New(task.WithAgent(...)) 注册子智能体
//   - 主智能体通过 task 工具的 agent 参数选择子智能体
//   - console.WithAgent 绑定外部构造的 Agent
//
// 运行：
//
//	go run ./examples/10-sub-agent
package main

import (
	"context"
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

	// ── 子智能体定义 ────────────────────────────────────────

	reviewer := ga.NewAgent(
		ga.WithName("reviewer"),
		ga.WithDescription("代码审查专家，给出结构、性能、安全性方面的改进建议"),
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt("你是资深代码审查专家。按严重程度排序，每条建议附修改示例。"),
	)

	translator := ga.NewAgent(
		ga.WithName("translator"),
		ga.WithDescription("技术文档翻译专家，中英互译，保留代码片段和专业术语"),
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt("你是技术文档翻译专家。代码片段和变量名保持原样不翻译。"),
	)

	// ── 注册 task 工具（带子智能体）────────────────────────

	taskTool, err := task.New(
		task.WithAgent(reviewer),
		task.WithAgent(translator),
	)
	if err != nil {
		panic(err)
	}

	// ── 主智能体（协调者）───────────────────────────────────

	agent := ga.NewAgent(
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt(
			"你是智能协调者。根据用户需求用 task 工具委派给子智能体：\n"+
				"- 代码审查 → agent 选 reviewer\n"+
				"- 翻译 → agent 选 translator\n"+
				"- 简单问答 → 直接回答\n"+
				"任务描述要自包含，子智能体看不到主会话历史。",
		),
		ga.WithTools(taskTool),
	)

	app := console.New(console.WithAgent(agent))
	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
