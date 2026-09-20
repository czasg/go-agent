// Package skill 提供技能系统 Hook：在 Agent 启动时注入 skill 工具与 system prompt，
// 模型通过调用 skill 工具按需加载技能指令（渐进式展示）。
//
// 技能遵循标准规范：每个技能是一个子目录，包含 SKILL.md 文件。
// frontmatter 提供元数据（name/description），正文是指令内容。
//
// 用法：
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithHooks(skill.New(
//	        skill.WithDir("/path/to/skills"),
//	    )),
//	)
package skill

import (
	"context"
	"path/filepath"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/config"
)

// Option 配置 Hook 的函数选项。
type Option func(*Hook)

// WithBackend 设置技能源后端。
func WithBackend(b Backend) Option {
	return func(h *Hook) {
		h.tool.b = b
	}
}

// WithDir 设置文件系统 Backend 的扫描根目录。
// dir 是工作目录，实际扫描 dir/skills/；若 dir 本身以 "skills" 结尾则直接使用。
func WithDir(dir string) Option {
	return func(h *Hook) {
		if filepath.Base(dir) == "skills" {
			h.dir = dir
			h.tool.b = NewFilesystemBackend(dir)
		} else {
			skillsDir := filepath.Join(dir, "skills")
			h.dir = skillsDir
			h.tool.b = NewFilesystemBackend(skillsDir)
		}
	}
}

// WithSystemPrompt 覆盖默认的 skill 系统提示词。
func WithSystemPrompt(prompt string) Option {
	return func(h *Hook) {
		h.instruction = prompt
	}
}

// WithContext 设置 Backend 调用使用的 context（默认 context.Background()）。
func WithContext(ctx context.Context) Option {
	return func(h *Hook) {
		h.tool.ctx = ctx
	}
}

// New 创建 skill Hook。
// 默认使用 FilesystemBackend（扫描 ~/.ga/skills/），可通过 WithBackend 或 WithDir 覆盖。
// system prompt 动态构建：注入 skills 目录路径，避免模型盲目猜测。
func New(opts ...Option) *Hook {
	h := &Hook{
		tool: &skillTool{},
		dir:  filepath.Join(config.Dir, "skills"),
	}
	for _, opt := range opts {
		opt(h)
	}
	if h.tool.b == nil {
		h.tool.b = NewFilesystemBackend(h.dir)
	}
	if h.tool.ctx == nil {
		h.tool.ctx = context.Background()
	}
	// 动态构建 system prompt（注入 skills 目录路径），除非用户通过 WithSystemPrompt 显式覆盖。
	if h.instruction == "" {
		h.instruction = buildSystemPrompt(h.dir)
	}
	return h
}

// ── 类型定义 ──────────────────────────────────────────────────

// FrontMatter 是 SKILL.md 的 YAML frontmatter 元数据。
type FrontMatter struct {
	Name        string `yaml:"name"        json:"name"`
	Description string `yaml:"description" json:"description"`
}

// Skill 是完整的技能实例：元数据 + 指令正文 + 文件路径。
type Skill struct {
	FrontMatter
	Content       string // 指令正文（已剥离 frontmatter）
	BaseDirectory string // SKILL.md 所在目录（辅助脚本的相对路径基准）
}

// Backend 是技能源的抽象接口。
// List 只返回轻量元数据（每轮 agent loop 都会调用）；
// Get 按需加载完整技能内容（模型调用 skill 工具时触发）。
type Backend interface {
	List(ctx context.Context) ([]FrontMatter, error)
	Get(ctx context.Context, name string) (Skill, error)
}

// ── Hook ──────────────────────────────────────────────────────

// Hook 实现 AgentStartHook，在 Agent 启动时注入 skill 工具与 system prompt。
type Hook struct {
	tool        *skillTool
	instruction string
	dir         string // skills 目录路径，用于 system prompt 注入
}

func (h *Hook) Name() string { return "skill" }

// OnAgentStart 注入 skill 工具到 Context.Tools，追加 skill system prompt。
func (h *Hook) OnAgentStart(c *ga.Context) {
	c.Tools.Register(h.tool)
	c.Messages.AppendSystemHint(h.instruction)
}
