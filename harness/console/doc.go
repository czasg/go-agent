// Package harness 是面向"终端会话"场景的小型上层框架：底层仍是 ga 的
// Agent + Session，只是把每个终端 demo 都会重写一遍的那部分收敛成可复用组件。
//
// 包内分层（自下而上，可单独使用）：
//
//	Theme     文案 + 配色，一切终端输出的样式来源
//	Console   唯一的终端 I/O 入口：读一行 / 提问 / 审批
//	Renderer  事件 → 终端文本（流式思考、正文、工具、统计）
//	App       把上面三者 + ga.Agent 缝成一个 REPL 程序
//
// 最小用法：
//
//	app := harness.New(
//	    harness.WithChatModel(model),
//	    harness.WithSystemPrompt("你是一个编程助手。"),
//	    harness.WithHooks(filesystem.New()),
//	)
//	if err := app.Run(context.Background()); err != nil {
//	    log.Fatal(err)
//	}
//
// 只要渲染、不要 REPL？单独用渲染器，任何 Agent 都能接：
//
//	renderer := harness.NewTerminalRenderer()
//	agent := ga.NewAgent(ga.WithChatModel(model), ga.WithOnEvent(renderer.OnEvent))
//
// 要 human-in-the-loop？Console 同时满足 ask.Prompter 和 approve.Approver，
// 与 REPL 共享同一个 stdin reader（这是 harness 存在的关键理由之一）：
//
//	console := harness.NewConsole()
//	askTool, _ := ask.New(console)
//	app := harness.New(
//	    harness.WithChatModel(model),
//	    harness.WithTools(askTool),
//	    harness.WithConsole(console),
//	    harness.WithToolMiddlewares(
//	        ga.ForTools(approve.New(console), "delete_file", "drop_table"),
//	    ),
//	)
//
// 边界说明：harness 不引入持久化、多租户、鉴权——那些属于业务侧，通过 ga 的
// hook / event 插进来（框架里没有一行 DB 代码）。harness 只解决"终端里跑起来
// 像个像样应用"这一件事，因此刻意不把 ga 的 Agent 组装逻辑（ga.Option）复制
// 一份新语义，而是原样转发 + 留 WithAgentOptions 逃生舱。
package console
