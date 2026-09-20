// Package offload 提供大结果卸载中间件：工具结果超过阈值时写入文件系统，
// 上下文只保留头部摘要与文件路径，防止大输出（日志、转储、目录遍历）
// 撑爆上下文。写入失败时降级透传原文，绝不阻断工具执行。
//
// 职责边界：本中间件只负责"大结果落地"，历史消息压缩由上层 Context Manager 负责。
//
// 用法：
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithToolMiddlewares(offload.New()),                      // 默认阈值 32KB
//	    // ga.WithToolMiddlewares(offload.New(offload.WithThreshold(64*1024))), // 自定义
//	    // ga.WithToolMiddlewares(offload.New(                        // 精细控制
//	    //     offload.WithThreshold(1024*64),
//	    //     offload.WithHead(1024),
//	    // )),
//	)
package offload

import (
	"fmt"
	"github.com/czasg/go-agent/config"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// 默认阈值（字节）。
const (
	DefaultThreshold = 32 * 1024 // 32KB — 超过即卸载
	DefaultHead      = 1024      // 1KB  — 保留头部长度
)

const baseDirName = "offload"

// Options 配置卸载行为。
type Options struct {
	// Threshold 卸载阈值（字节）：结果超过此值触发卸载。
	// 默认 32KB。设为 0 使用默认值。
	Threshold int
	// Head 保留在上下文中的头部长度（字节）。
	// 截取结果开头作为摘要线索，模型可据此判断是否需要按需读取全文。默认 1024。
	Head int
	// Dir 卸载目标目录，默认 "~/.ga/offload"。
	Dir string
	// Skip 免卸载工具名列表：这些工具的结果原样保留。
	Skip []string
}

// Option 配置卸载行为。
type Option func(*Options)

// WithThreshold 设置卸载阈值（字节）。
func WithThreshold(n int) Option {
	return func(o *Options) { o.Threshold = n }
}

// WithHead 设置保留的头部长度（字节）。
func WithHead(n int) Option {
	return func(o *Options) { o.Head = n }
}

// WithDir 设置卸载目录。
func WithDir(dir string) Option {
	return func(o *Options) { o.Dir = dir }
}

// WithSkip 设置免卸载工具名列表。
func WithSkip(names ...string) Option {
	return func(o *Options) { o.Skip = names }
}

// New 返回大结果卸载中间件。
func New(opts ...Option) ga.ToolMiddleware {
	o := Options{
		Threshold: DefaultThreshold,
		Head:      DefaultHead,
		Dir:       config.Dir,
	}
	for _, fn := range opts {
		fn(&o)
	}

	// 预计算卸载目录的绝对路径（只算一次）。
	if !strings.EqualFold(filepath.Base(o.Dir), baseDirName) {
		o.Dir = filepath.Join(o.Dir, baseDirName)
	}
	absDir, err := filepath.Abs(o.Dir)
	if err != nil {
		absDir = o.Dir
	}
	o.Dir = absDir

	return func(next ga.ToolHandler) ga.ToolHandler {
		return func(c *ga.Context, call *schema.ToolCall) (string, error) {
			result, err := next(c, call)
			if err != nil {
				return result, err
			}

			// Skip 白名单：原样返回。
			if slices.Contains(o.Skip, call.Function.Name) {
				return result, nil
			}

			// 超过阈值 → 卸载。
			if len(result) > o.Threshold {
				return o.doOffload(call.ID, result), nil
			}

			return result, nil
		}
	}
}

// doOffload 执行卸载：写入文件，返回摘要+路径。写入失败降级透传原文。
func (o *Options) doOffload(callID, result string) string {
	path, ok := o.offload(callID, result)
	if !ok {
		return result
	}
	head := result
	if len(head) > o.Head {
		head = head[:o.Head]
	}
	return fmt.Sprintf("%s\n\n[输出共 %d 字节，已卸载到 %s，可用文件工具按需读取]",
		head, len(result), path)
}

// offload 把内容写入文件系统，返回绝对路径与是否成功。
// 文件名直接用 callID，一一对应，并发安全。
func (o *Options) offload(callID, content string) (string, bool) {
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return "", false
	}
	name := callID + ".offload"
	path := filepath.Join(o.Dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", false
	}
	return path, true
}
