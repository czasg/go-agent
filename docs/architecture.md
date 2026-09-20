# 架构概览

本文档描述 go-agent 框架的核心架构、执行流程和设计原则。细节留给代码注释和 [examples](../examples/)。

## 分层架构

```
┌─────────────────────────────────────────────────────┐
│                   harness 层                         │
│            console.REPL · 渲染 · 交互                │
├─────────────────────────────────────────────────────┤
│                   ext 扩展层                          │
│     skill · todos · summary · task · approve · ...  │
├─────────────────────────────────────────────────────┤
│                   核心引擎层                          │
│  agent · context · message · tool · hook · event    │
├─────────────────────────────────────────────────────┤
│                   LLM 提供者层                        │
│           OpenAI · Anthropic · (可扩展)              │
├─────────────────────────────────────────────────────┤
│                   协议层                              │
│              MCP Client · JSON Schema                │
└─────────────────────────────────────────────────────┘
```

- **核心引擎层**：无状态 Agent Loop，定义了框架的所有核心契约
- **ext 扩展层**：基于 Hook/Middleware/Tool 三种扩展点构建的开箱即用组件
- **harness 层**：面向终端用户的交互壳（REPL、渲染、输入输出）
- **LLM 提供者层**：统一接口 `BaseModel.Chat()`，各厂商独立实现
- **协议层**：MCP 客户端接入远程工具，JSON Schema 反射生成参数描述

## Agent Loop 执行流程

`Agent.Run(ctx, history)` 是框架的核心入口。整个执行流程如下：

```
输入: []*Message (对话历史)
         │
         ▼
   ┌─── AgentStart Hooks ───┐  ← 初始化校验、注入工具/技能
   │                         │
   │  ┌─── 循环开始 ───┐     │
   │  │                 │     │
   │  │  ClearTransient │     │  ← 清除上轮瞬态消息
   │  │  Fix 消息修补    │     │  ← 补缺删孤，保证历史完整
   │  │                 │     │
   │  │  ctx 取消检查    │     │
   │  │                 │     │
   │  │  IterationStart │     │  ← 上下文压缩、动态注入
   │  │    Hooks        │     │
   │  │                 │     │
   │  │  Model 链       │     │  ← ModelMiddleware → LLM.Chat
   │  │    ↓            │     │
   │  │  流式回调       │     │  ← MessageDelta Hooks + OnEvent
   │  │    ↓            │     │
   │  │  MessageEnd     │     │  ← 内容安全、审计
   │  │    Hooks        │     │
   │  │                 │     │
   │  │  有 ToolCalls?  │     │
   │  │    ├─ 否 → 退出  │     │
   │  │    └─ 是 ↓       │     │
   │  │                 │     │
   │  │  并发执行 Tools  │     │  ← ToolMiddleware → Tool.Execute
   │  │                 │     │
   │  │  IterationEnd   │     │  ← 轮次预算、质量检查
   │  │    Hooks        │     │
   │  │                 │     │
   │  │  Steering       │     │  ← 用户插队消息注入
   │  │    Messages     │     │
   │  │                 │     │
   │  └── 有 Abort? ────┘     │  ← 检查是否被终止
   │           │ 否则继续循环   │
   │                         │
   └─── AgentEnd Hooks ──────┘  ← 资源清理、持久化（defer 语义）
         │
         ▼
   输出: RunResult
```

## 核心组件

### Agent — 不可变配置载体

`agent.go` 定义了 Agent 结构体，构造完成后不可变，可并发复用。

核心职责：
- 持有 model、tools、hooks、middlewares、systemPrompt
- `Run()` 创建 Context 并启动 RunLoop
- `RunLoop()` 执行完整的 model ↔ tool 循环

### Context — 运行时状态

`context.go` 定义了每次 Run 的可变状态。嵌入 `context.Context`，支持外部取消。

核心职责：
- 持有 Messages、Tools（私有副本）、Iteration、Message
- `Abort()` / `AbortWithError()` 请求终止 loop（并发安全）
- `Emit()` 发送观察事件
- `Set()` / `Get()` 跨组件共享数据

**设计铁律**：返回值只表达"结果"，控制流只走 Context。

### MessageStore — 消息历史管理

`message.go` 提供消息历史的便利管理。

核心能力：
- `Messages()` 返回模型可见消息（摘要裁剪后）
- `Fix()` 双向修补（补缺 tool 结果、删孤 tool 消息）
- `ClearTransient()` 移除瞬态消息
- `Rounds()` / `ExtractBeforeRounds()` 轮次切分与提取
- `TotalTokens()` token 估算

### Tool — 工具系统

`tool.go` 定义工具接口与注册表。

```go
type BaseTool interface {
    Info() schema.ToolInfo
    Execute(ctx *Context, call *schema.ToolCall) (string, error)
}
```

- `NewTool[T]`：泛型适配器，从 struct tag 自动生成 JSON Schema
- `ToolStore`：注册表，支持白名单/黑名单可见性控制、浅拷贝、并发安全

### Hook — 生命周期切面

`hook.go` 定义 6 个生命周期 Hook 接口。实现者只需选择性实现关心的接口，`WithHooks` 通过类型断言自动挂载。

Hook 只通过 `c.Abort()` 影响控制流，不返回 error。除 `MessageDeltaHook` 外，均在主 loop 单 goroutine 串行执行。

### Middleware — 洋葱模型

`middleware.go` 定义两套中间件：

- `ToolMiddleware`：包住一次工具执行（审批、限流、日志）
- `ModelMiddleware`：包住一次模型调用（重试、计费、缓存）

装饰器天然自带"中断"能力——不调用 `next` 就等于中断。

`ForTools(mw, names...)` 可将中间件限定到特定工具名。

### Event — 观察系统

`event.go` 定义 8 种事件类型，与 Hook 平级但职责分离：

| 扩展方式 | 改状态 | 只观察 | 并发模型 |
|----------|--------|--------|----------|
| Hook | ✅ c.Abort() | ✅ | 主 loop 串行 |
| Middleware | ✅ 不调 next | ✅ | 随 tool/model |
| Event | ❌ | ✅ | 主 loop 串行 |

`EventHandler` 提供类型安全的便捷分发，`OnEventChannel()` 支持 channel 消费。

## 设计原则

### 1. 无状态 Agent，有状态 Context

Agent 是不可变配置，Run 不碰它的任何字段。所有运行时可变状态都在 Context 里，每次 Run 独立创建。这使得同一个 Agent 可被多个 goroutine 并发复用。

### 2. 控制流只走 Context

`Abort()` / `AbortWithError()` 是唯一的主动终止方式，同时 cancel 内部 context 使流式调用立即中断。error 只表达"结果"，不表达"终止"。

### 3. 返回值只表达结果

Tool.Execute 的 `(string, error)` 只表示这次调用的结果。error 被格式化后回喂模型，loop 继续而非终止。要终止 loop 请调用 `c.Abort()`。

### 4. 种子与副本分离

Agent 持有"种子" ToolStore，每次 Run 通过 `Copy()` 创建私有副本。Hook/Tool 可在运行期动态注册工具到副本上，不影响 Agent 种子——并发安全。

### 5. 并发工具执行

同一轮的多个 tool_calls 并发执行（`sync.WaitGroup`），各自把结果写回自己的 ToolCall。ToolStore 用 RWMutex 保护，支持工具执行中动态注册新工具。

### 6. Hook 与 Event 职责分离

Hook 管"拦截/改状态"（切面），Event 管"观察/记录"（日志、渲染、落库）。需要改变执行流用 Hook，只需要看用 Event。

## 扩展点总览

```
                    扩展方式
          ┌──────────┼──────────┐
          │          │          │
        Hook    Middleware    Tool
          │          │          │
    ┌─────┴─────┐    │    ┌────┴────┐
    │           │    │    │         │
 Agent级    Iteration级  │    task    ask
 (Start/End) (Start/End) │   (子智能体) (提问)
                         │
              ┌──────────┴──────────┐
              │                     │
          ToolMiddleware      ModelMiddleware
              │                     │
        approve · limit        (自定义)
        offload
```

- **需要在固定时间点拦截/改状态** → Hook
- **需要包住一次操作（能控制要不要跑、跑几次）** → Middleware
- **需要给模型提供新能力** → Tool
- **只需要观察执行过程** → Event
