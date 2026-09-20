# Extensions

go-agent 的扩展包，按接入形态分三类，通过 `ga.WithHooks` / `ga.WithToolMiddlewares` / `ga.WithTools` 接入。

## Hook — `ga.WithHooks`

生命周期钩子，在 Agent 启动/迭代/结束时注入行为。

| 包 | 说明 | 接入 |
|---|------|------|
| [filesystem](hook/filesystem/) | `read_file` `write_file` `edit_file` `terminal` 四个文件/终端工具 | `filesystem.New()` |
| [summary](hook/summary/) | 上下文摘要压缩，防止消息历史溢出 | `summary.New(opts...)` |
| [todos](hook/todos/) | `todo_list` 工具 + 规划提示词，追踪子任务进度 | `todos.New()` |
| [skill](hook/skill/) | `skill` 工具，按需加载技能指令（渐进式展示） | `skill.New(opts...)` |
| [repetition](hook/repetition/) | 重复输出检测，触发后立即中止模型调用 | `repetition.New()` |
| [toolsearch](hook/toolsearch/) | `tool_search` 元工具，正则按需发现并加载工具 | `toolsearch.New(allowed...)` |

## Middleware — `ga.WithToolMiddlewares`

工具中间件，拦截/增强工具调用链。

| 包 | 说明 | 接入 |
|---|------|------|
| [limit](middleware/limit/) | 并发限流，信号量限制同时执行的工具数 | `limit.New(3)` |
| [approve](middleware/approve/) | 审批拦截，危险工具执行前阻塞等待确认 | `ga.ForTools(approve.New(approver), "tool")` |
| [offload](middleware/offload/) | 大结果卸载，超阈值写文件系统，上下文只留摘要 | `offload.New()` |

## Tool — `ga.WithTools`

独立工具，由模型主动调用。

| 包 | 说明 | 接入 |
|---|------|------|
| [task](tool/task/) | 子任务（克隆当前 Agent）/ 子智能体（委托指定 Agent） | `task.New()` |
| [ask](tool/ask/) | 向用户提问并阻塞等待回答，支持结构化多选 | `ask.New(prompter)` |

## 组装示例

```go
agent := ga.NewAgent(
    ga.WithChatModel(model),
    ga.WithHooks(
        filesystem.New(),
        todos.New(),
        repetition.New(),
    ),
    ga.WithTools(taskTool),
    ga.WithToolMiddlewares(
        limit.New(3),
        ga.ForTools(approve.New(approver), "terminal"),
    ),
)
```

每个包的详细配置项见各自目录下的源码注释。
