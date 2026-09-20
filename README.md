# go-agent

> Go 语言轻量级 AI Agent 框架 —— 多模型、工具调用、生命周期钩子、MCP 协议，开箱即用。

[![Go Version](https://img.shields.io/github/go-mod/go-version/czasg/go-agent)](https://go.dev/)
[![CI](https://github.com/czasg/go-agent/actions/workflows/go.yml/badge.svg)](https://github.com/czasg/go-agent/actions/workflows/go.yml)
[![License](https://img.shields.io/github/license/czasg/go-agent)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/czasg/go-agent)](https://goreportcard.com/report/github.com/czasg/go-agent)

---

## 核心特性

- **无状态 Agent Loop** — `Agent.Run(ctx, history)` 进去一份历史、出来一份结果，天然支持并发复用、多轮对话、挂起恢复
- **泛型类型化工具** — `NewTool[T]` 一行代码定义工具，struct tag 自动生成 JSON Schema，告别手写样板
- **Hook + Middleware 双扩展** — 6 个生命周期 Hook 管"切面"，Tool/Model Middleware 管"洋葱"，各司其职
- **MCP 协议支持** — 一个 `mcp.Client` 接入远程 MCP Server，工具零改动流入 Agent Loop
- **子智能体与任务委托** — `task` 工具支持克隆模式和 Sub-Agent 模式，隔离上下文、分治复杂任务
- **终端 Harness** — `harness/console` 提供 REPL、流式渲染、Human-in-the-loop、Ctrl+C 打断，一行代码跑起来

## 安装

```bash
go get github.com/czasg/go-agent@latest
```

要求 Go 1.24+。

## 快速开始

### 1. 配置模型

首次运行自动在 `~/.ga/settings.json` 生成默认配置，填入 API Key 即可：

```json
{
  "llm": {
    "provider": "openai",
    "base_url": "https://api.openai.com/v1",
    "api_key": "",
    "model": "gpt-4o"
  }
}
```

支持 `openai` 和 `anthropic` 两种 provider。

### 2. 最简示例：3 行跑起一个 Agent

```go
package main

import (
    "context"
    "log"

    ga "github.com/czasg/go-agent"
    "github.com/czasg/go-agent/config"
    "github.com/czasg/go-agent/harness/console"
)

func main() {
    cfg := config.GetConfig()
    model, _ := ga.NewChatModel(&cfg.LLM)

    agent := ga.NewAgent(
        ga.WithChatModel(model),
        ga.WithSystemPrompt("你是一个有用的助手。"),
    )

    app := console.New(console.WithAgent(agent))
    if err := app.Run(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

```bash
go run main.go
```

### 3. 给 Agent 加工具

```go
type WeatherParams struct {
    City string `json:"city" jsonschema:"description=城市名称"`
}

weatherTool, _ := ga.NewTool("get_weather", "查询指定城市的天气", 
    func(ctx *ga.Context, in WeatherParams) (string, error) {
        return fmt.Sprintf("%s: 晴，25°C", in.City), nil
    })

agent := ga.NewAgent(
    ga.WithChatModel(model),
    ga.WithTools(weatherTool),
)
```

工具参数自动从 struct tag 生成 JSON Schema，模型调用时自动反序列化为类型化结构体。

## 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│                        Agent (不可变配置)                      │
│  model · tools · hooks · middlewares · systemPrompt         │
└──────────────────────────┬──────────────────────────────────┘
                           │ Run(ctx, history)
                           ▼
┌─────────────────────────────────────────────────────────────┐
│                     Context (本次运行状态)                     │
│  messages · tools(副本) · iteration · abort                  │
└──────────────────────────┬──────────────────────────────────┘
                           │
                           ▼
            ┌──── AgentStart Hooks ────┐
            │                          │
            ▼                          │
   ┌─ IterationStart Hooks ─┐         │
   │                         │         │
   │   Model Middleware 链    │         │
   │       ↓                 │         │
   │   调用 LLM (流式)       │  循环    │
   │       ↓                 │         │
   │   MessageEnd Hooks      │         │
   │       ↓                 │         │
   │   Tool Middleware 链    │         │
   │       ↓                 │         │
   │   并发执行 Tools         │         │
   │       ↓                 │         │
   │   IterationEnd Hooks    │         │
   │       ↓                 │         │
   └─ 有 tool_calls? ─是────┘         │
            │ 否                       │
            ▼                          │
            └──── AgentEnd Hooks ──────┘
                           │
                           ▼
                      RunResult
```

详细架构文档见 [docs/architecture.md](docs/architecture.md)。

## 核心概念

### Agent

不可变的配置载体。构造完成后可被多个 goroutine 并发复用：

```go
agent := ga.NewAgent(
    ga.WithChatModel(model),
    ga.WithName("assistant"),
    ga.WithDescription("通用助手"),
    ga.WithMaxIterations(32),
    ga.WithSystemPrompt("..."),
    ga.WithTools(tools...),
)
```

### Context

每次 `Run` 的运行时状态。贯穿整个 Agent Loop，hook/tool/middleware 都通过它交互：

```go
// 在 tool 或 hook 中
c.Abort()                    // 请求终止 loop
c.AbortWithError(err)        // 带错误终止
c.Messages.AppendSystemHint  // 注入系统提示
c.Tools.Register(dynamicTool) // 动态注册工具
c.Emit(customEvent, data)    // 发送自定义事件
c.Set("key", value)          // 跨组件共享数据
```

### Tool

两种定义方式：

```go
// 方式一：泛型工具（推荐）
tool, _ := ga.NewTool("search", "搜索文档", func(ctx *ga.Context, in struct {
    Query string `json:"query" jsonschema:"description=搜索关键词"`
}) (string, error) {
    return "search results...", nil
})

// 方式二：实现 BaseTool 接口
type MyTool struct{}
func (t *MyTool) Info() schema.ToolInfo { return schema.ToolInfo{...} }
func (t *MyTool) Execute(ctx *ga.Context, call *schema.ToolCall) (string, error) { ... }
```

### Hook

6 个生命周期钩子，通过类型断言自动挂载：

| Hook | 触发时机 | 典型用途 |
|------|----------|----------|
| `AgentStartHook` | Run 开始前 | 初始化校验、注入工具 |
| `IterationStartHook` | 每轮模型调用前 | 上下文压缩、动态注入 |
| `MessageDeltaHook` | 流式输出每个增量 | 重复检测、实时过滤 |
| `MessageEndHook` | 模型返回完整响应后 | 内容安全、审计 |
| `IterationEndHook` | 工具执行完毕后 | 轮次预算、质量检查 |
| `AgentEndHook` | Run 结束（含异常） | 资源清理、持久化 |

```go
type MyHook struct{}
func (h *MyHook) Name() string { return "my_hook" }
func (h *MyHook) OnIterationStart(c *ga.Context) {
    // 每轮开始前的逻辑
}
```

### Middleware

洋葱模型，包住一次操作，比 Hook 更强——能决定内层要不要跑、跑几次：

```go
agent := ga.NewAgent(
    ga.WithToolMiddlewares(
        ga.ForTools(approve.New(console), "delete_file"), // 只拦截危险工具
        limit.New(3),                                      // 全局并发限制
        offload.New(),                                     // 大结果卸载
    ),
    ga.WithModelMiddlewares(
        // 重试、限流、计费...
    ),
)
```

### Event

观察系统，只看不改。与 Hook 平级但职责分离：

```go
agent := ga.NewAgent(
    ga.WithOnEvent(ga.EventHandler{
        OnMessageEndHandler: func(msg *ga.Message) {
            fmt.Println("模型回复:", msg.Content)
        },
        OnToolStartHandler: func(call *ga.ToolCall) {
            fmt.Println("调用工具:", call.Function.Name)
        },
    }.OnEvent()),
)
```

## 扩展能力

框架在 `ext/` 下提供开箱即用的扩展组件：

| 包 | 类型 | 功能 |
|---|------|------|
| `ext/hook/skill` | Hook | 技能系统：按需加载 SKILL.md 指令 |
| `ext/hook/todos` | Hook | 任务规划：结构化待办清单追踪 |
| `ext/hook/summary` | Hook | 上下文压缩：超 token 阈值自动摘要 |
| `ext/hook/repetition` | Hook | 重复检测：流式输出中实时检测循环 |
| `ext/hook/toolsearch` | Hook | 工具搜索：正则匹配按需加载工具 |
| `ext/tool/task` | Tool | 任务委托：克隆或指定子智能体跑子任务 |
| `ext/tool/ask` | Tool | 向用户提问：结构化问题 + 选项 |
| `ext/middleware/approve` | Middleware | 审批拦截：危险工具执行前请求确认 |
| `ext/middleware/offload` | Middleware | 大结果卸载：超阈值写文件系统 |
| `ext/middleware/limit` | Middleware | 并发限流：信号量控制工具并发数 |

## 学习路径

从简到繁，逐步掌握框架能力。每个示例可独立运行：

| # | 示例 | 概念 | 难度 |
|---|------|------|------|
| 01 | [llm-chat](examples/01-llm-chat/) | 纯 LLM 调用、流式输出 | ⭐ |
| 02 | [agent-chat](examples/02-agent-chat/) | Agent + Session + 终端交互 | ⭐ |
| 03 | [custom-tool](examples/03-custom-tool/) | 手写 BaseTool 接口 | ⭐⭐ |
| 04 | [typed-tool](examples/04-typed-tool/) | 泛型工具 NewTool[T] | ⭐⭐ |
| 05 | [filesystem](examples/05-filesystem/) | Hook 注入工具 | ⭐⭐ |
| 06 | [hook-lifecycle](examples/06-hook-lifecycle/) | Hook 触发顺序与 Abort | ⭐⭐ |
| 07 | [event-streaming](examples/07-event-streaming/) | Event 观察与流式统计 | ⭐⭐ |
| 08 | [ask](examples/08-ask/) | Human-in-the-loop | ⭐⭐ |
| 09 | [approve](examples/09-approve/) | 审批中间件 | ⭐⭐⭐ |
| 10 | [sub-agent](examples/10-sub-agent/) | Sub-Agent 模式 | ⭐⭐⭐ |
| 11 | [task](examples/11-task/) | Task 克隆模式 | ⭐⭐⭐ |
| 12 | [skill](examples/12-skill/) | 技能系统 | ⭐⭐ |
| 13 | [todos](examples/13-todos/) | 任务规划 | ⭐⭐ |
| 14 | [toolsearch](examples/14-toolsearch/) | 工具搜索 | ⭐⭐ |
| 15 | [multimodal](examples/15-multimodal/) | 多模态输入 | ⭐⭐ |

```bash
go run ./examples/01-llm-chat
go run ./examples/02-agent-chat
# ...
```

## 目录结构

```
go-agent/
├── agent.go              # Agent 核心：构造、RunLoop、工具并发执行
├── context.go            # Context：运行时状态、Abort、事件发射
├── message.go            # MessageStore：消息管理、摘要裁剪、轮次切分
├── tool.go               # Tool 接口、NewTool[T]、ToolStore 注册表
├── hook.go               # 6 个生命周期 Hook 接口及执行器
├── middleware.go          # Tool/Model Middleware 洋葱链
├── event.go              # Event 类型与 EventHandler
├── session.go            # Session 便利壳（多轮历史管理）
├── factory.go            # ChatModel 工厂（OpenAI / Anthropic）
├── types.go              # 类型重导出
├── schema/               # 核心类型定义
│   ├── message.go        #   Message、ContentPart、MessageDelta
│   ├── tool.go           #   ToolCall、ToolInfo
│   ├── schema.go         #   Parameters 反射生成 JSON Schema
│   └── run.go            #   RunResult、TokenUsage、Timing
├── llm/                  # LLM 提供者
│   ├── model.go          #   BaseModel 接口、Option
│   ├── provider_openai.go
│   └── provider_anthropic.go
├── config/               # 配置
├── mcp/                  # MCP 协议客户端
├── ext/                  # 扩展组件
│   ├── hook/             #   生命周期 Hook
│   ├── tool/             #   内置工具
│   └── middleware/       #   中间件
├── harness/              # 上层框架
│   └── console/          #   终端 REPL + 渲染
└── examples/             # 15 个渐进式示例
```

## 文档

- [架构概览](docs/architecture.md) — 核心组件、执行流程、设计原则
- [扩展组件](ext/README.md) — Hook / Middleware / Tool 扩展的详细用法与代码示例
- [示例代码](examples/) — 15 个可独立运行的渐进式示例

## 参与贡献

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/xxx`)
3. 提交改动 (`git commit -m 'feat: add xxx'`)
4. 推送分支 (`git push origin feature/xxx`)
5. 提交 Pull Request

运行测试：

```bash
go test -race ./...
```

## License

[MIT](LICENSE)
