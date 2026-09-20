// Approve 中间件 —— 展示 ForTools + approve 拦截危险工具。
//
// 展示：
//   - approve.New：框架级审批中间件，拦截指定工具的执行
//   - ga.ForTools：把审批限定到指定工具，其余工具不拦截
//   - 模型完全无感：它调用 delete_file 时，中间件自动请求审批
//
// 运行：
//
//	go run ./examples/09-approve
package main

import (
	"context"
	"log"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/ext/middleware/approve"
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

	// 演示用：一个普通工具，受审批保护。
	deleteTool, _ := ga.NewTool("delete_file",
		"删除指定文件",
		func(c *ga.Context, in struct {
			Path string `json:"path" jsonschema:"description=要删除的文件路径"`
		}) (string, error) {
			return "已删除: " + in.Path, nil
		})

	// 演示用：一个无害工具，不受审批保护。
	readTool, _ := ga.NewTool("read_file",
		"读取指定文件内容",
		func(c *ga.Context, in struct {
			Path string `json:"path" jsonschema:"description=要读取的文件路径"`
		}) (string, error) {
			return "文件内容: ...", nil
		})

	agent := ga.NewAgent(
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt(
			"你是一个编程助手，可以读写文件。\n"+
				"- delete_file 受审批保护，调用时会自动请求用户审批\n"+
				"- read_file 可以直接调用，无需审批\n",
		),
		ga.WithTools(deleteTool, readTool),
		// 框架级拦截：delete_file 必须经过审批，模型无法绕过。
		// read_file 不在列表中，直接放行。
		ga.WithToolMiddlewares(
			ga.ForTools(approve.New(c), "delete_file"),
		),
	)

	app := console.New(
		console.WithAgent(agent),
		console.WithConsole(c),
	)

	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
