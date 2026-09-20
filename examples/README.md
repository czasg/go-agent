# Examples

从简到繁，逐步展示 go-agent 的核心能力。每个示例可独立运行。

## 学习路径

| # | 目录 | 概念 | 难度 |
|---|------|------|------|
| 01 | [llm-chat](01-llm-chat/) | 纯 LLM 调用、流式输出、手动管理历史 | ⭐ |
| 02 | [agent-chat](02-agent-chat/) | Agent、Session、SystemPrompt、OnEvent | ⭐ |
| 03 | [custom-tool](03-custom-tool/) | 手写 BaseTool 接口、JSON Schema、参数解析 | ⭐⭐ |
| 04 | [typed-tool](04-typed-tool/) | NewTool[T] 泛型工具、结构体 tag 自动生成 schema | ⭐⭐ |
| 05 | [filesystem](05-filesystem/) | Hook 注入工具、filesystem 内置文件/终端工具 | ⭐⭐ |
| 06 | [hook-lifecycle](06-hook-lifecycle/) | 五种 Hook 触发顺序、c.Abort() | ⭐⭐ |
| 07 | [event-streaming](07-event-streaming/) | EventHandler、OnEventChannel、流式统计 | ⭐⭐ |
| 08 | [ask](08-ask/) | Human-in-the-loop、ask 工具 | ⭐⭐ |
| 09 | [approve](09-approve/) | 框架级审批中间件、ForTools | ⭐⭐⭐ |
| 10 | [sub-agent](10-sub-agent/) | Sub-Agent 模式、task.WithAgent | ⭐⭐⭐ |
| 11 | [task](11-task/) | Task 克隆模式、隔离子任务 | ⭐⭐⭐ |
| 12 | [skill](12-skill/) | skill 技能系统、按需加载指令 | ⭐⭐ |
| 13 | [todos](13-todos/) | todo_list 工具、规划追踪、自定义事件 | ⭐⭐ |
| 14 | [harness-terminal](14-harness-terminal/) | harness 上层框架：REPL + 渲染 + 交互一站式 | ⭐ |

## 快速开始

配置文件路径：`~/.ga/settings.json`，首次运行自动初始化。

```bash
go run ./examples/01-llm-chat
go run ./examples/02-agent-chat
```
