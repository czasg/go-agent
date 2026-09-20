// Package filesystem 提供四个原子文件/终端工具：read_file、write_file、
// edit_file、terminal。它以 AgentStartHook 的形态接入——在 Run 开始、第一次
// 调模型之前，把四个工具注入到本次 Run 私有的 c.Tools（见 context.go），
// 并往 system 消息拼一段当前操作系统与工作区提示。
//
// 一键加载：
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithHooks(filesystem.New()),                       // 默认工作区 = 进程 cwd
//	    // ga.WithHooks(filesystem.New(filesystem.WithDir("/path"))),
//	)
//
// 这个便利入口必须放在本包、而不是核心 ga 包：ga 是不可变配置，不提供
// "运行时给 Agent 加 hook"的方法（会破坏并发复用与 Fork 契约）；且
// filesystem 已 import ga，反向依赖会造成 import 循环。经通用的 WithHooks
// 接入，与 mcp/skill 等扩展完全同构。
//
// 工作区（workspace）：四个工具共享同一个 workspace 根。相对路径一律相对
// workspace 解析，绝对路径原样使用——read/write/edit/terminal 落点一致，
// 不再出现"终端在 A 目录、读写却在进程 cwd"的错位。默认取 os.Getwd()。
//
// 目录浏览与内容搜索不单独做工具——交给 terminal（Windows 用 dir/findstr，
// 类 Unix 用 ls/grep）。terminal 有真实副作用，生产组装建议配
// ForTools(approve...) 之类的审批中间件。
package filesystem

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/czasg/go-agent"
)

// Hook 持有四个工具共享的状态：构造好的工具列表、workspace 根、以及 per-path
// 修改队列。构造后除锁外只读，可安全用于并发 Run。
type Hook struct {
	tools     []ga.BaseTool
	workspace string // 工作区根：相对路径的解析基准、terminal 的执行目录

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per-path 修改队列：同一文件的写/改串行
}

// New 构造 filesystem hook 并一次性构造四个工具。工具 schema 由参数结构体的
// tag 反射生成，反射失败属于编程错误（tag 写错），故这里 panic 而非返回
// error——与 demo 的 schema.MustParameters / MustInferToolInfo 一致，让
// WithHooks(filesystem.New()) 成为干净的一行。
func New(opts ...Option) *Hook {
	h := &Hook{locks: make(map[string]*sync.Mutex)}
	for _, o := range opts {
		o(h)
	}
	if h.workspace != "" {
		_ = os.MkdirAll(h.workspace, 0o755)
	}
	h.tools = []ga.BaseTool{
		mustTool(readTool(h)),
		mustTool(writeTool(h)),
		mustTool(editTool(h)),
		mustTool(terminalTool(h)),
	}
	return h
}

// mustTool 把 ga.NewTool 的 (BaseTool, error) 收敛为 panic 版：静态工具的
// schema 反射出错是编程错误，构造期直接暴露。
func mustTool(t ga.BaseTool, err error) ga.BaseTool {
	if err != nil {
		panic("filesystem: 构造工具失败: " + err.Error())
	}
	return t
}

// Option 是 hook 装配选项。
type Option func(*Hook)

// WithDir 设定工作区根（相对路径解析基准 + terminal 执行目录）。
// 空串保持默认（进程 cwd）。
func WithDir(dir string) Option {
	return func(h *Hook) { h.workspace = dir }
}

func (h *Hook) Name() string { return "filesystem" }

// OnAgentStart 把四个工具注入本次 Run 的工具表，并注入 OS/工作区提示。
// 注入的是 c.Tools（Run 私有副本），不碰 Agent 种子——并发 Run、Fork 均安全。
func (h *Hook) OnAgentStart(c *ga.Context) {
	for _, t := range h.tools {
		c.Tools.Register(t)
	}
	injectEnvHint(c, h.workspace)
}

// injectEnvHint 把系统与工作区信息拼进 system 消息（与 demo"system 只有一条、
// 始终在最前"的政策一致）：terminal 用系统原生 shell 执行，模型据此书写对应
// 语法；工作区路径让模型知道相对路径的落点。GOOS 与 workspace 进程内恒定，
// 注入内容每轮一致，不影响前缀缓存。
func injectEnvHint(c *ga.Context, workspace string) {
	hint := "# 运行环境\n" +
		"操作系统：" + runtime.GOOS +
		"（terminal 用系统原生 shell：Windows 写 cmd 语法 dir/type/findstr，类 Unix 写 POSIX 语法 ls/cat/grep）"
	if workspace != "" {
		hint += "\n工作区：" + workspace + "（文件工具的相对路径以此为基准，绝对路径原样使用）"
	}
	c.Messages.AppendSystemHint(hint)
}

// resolve 把工具收到的路径解析成实际文件系统路径：绝对路径原样返回，相对
// 路径相对 workspace 拼接（workspace 为空时退化为进程 cwd 相对，即原样）。
func (h *Hook) resolve(path string) string {
	if h.workspace == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(h.workspace, path)
}

// lockPath 返回路径级修改队列锁：同一文件的写/改串行，不同文件并行。
// 工具是并发执行的（见 agent.go 的 runTools），同一轮里模型可能同时对一个
// 文件发起 write 与 edit，串行化避免互相覆盖。锁以解析后的实际路径为键。
func (h *Hook) lockPath(path string) func() {
	h.mu.Lock()
	l, ok := h.locks[path]
	if !ok {
		l = &sync.Mutex{}
		h.locks[path] = l
	}
	h.mu.Unlock()
	l.Lock()
	return l.Unlock
}
