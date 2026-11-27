package offload

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	ga "github.com/czasg/go-agent"
)

const (
	DefaultThreshold = 4096
	DefaultHead      = 512
	DefaultDir       = "~/.ga/offload"
)

type Options struct {
	Threshold int
	Head      int
	Dir       string
	Skip      []string
}

func WithThreshold(n int) func(*Options) {
	return func(o *Options) {
		if n > 0 {
			o.Threshold = n
		}
	}
}

func WithHead(n int) func(*Options) {
	return func(o *Options) {
		if n > 0 {
			o.Head = n
		}
	}
}

func WithDir(dir string) func(*Options) {
	return func(o *Options) {
		if dir = strings.TrimSpace(dir); dir != "" {
			o.Dir = dir
		}
	}
}

func WithSkip(names ...string) func(*Options) {
	return func(o *Options) { o.Skip = names }
}

type Hook struct {
	opts Options
}

func New(opts ...func(*Options)) *Hook {
	o := Options{Threshold: DefaultThreshold, Head: DefaultHead, Dir: DefaultDir}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	if o.Threshold <= 0 {
		o.Threshold = DefaultThreshold
	}
	if o.Head <= 0 {
		o.Head = DefaultHead
	}
	if o.Dir == "" {
		o.Dir = DefaultDir
	}
	return &Hook{opts: o}
}

func (h *Hook) OnToolEnd(c *ga.Context, call *ga.ToolCall, res *ga.ToolResult) ga.Decision {
	if res == nil || res.IsError || len(res.MultiContent) > 0 || call == nil {
		return ga.Next()
	}
	runes := []rune(res.Content)
	if len(runes) <= h.opts.Threshold {
		return ga.Next()
	}
	if slices.Contains(h.opts.Skip, call.Name) {
		return ga.Next()
	}

	path, err := h.write(call.Name, res.Content)
	if err != nil {
		return ga.Next()
	}

	head := len(runes)
	if head > h.opts.Head {
		head = h.opts.Head
	}
	res.Content = fmt.Sprintf("%s\n\n[输出共 %d 字符，超过 %d 字符阈值，已卸载到 %s，可用文件工具按需读取全文]",
		string(runes[:head]), len(runes), h.opts.Threshold, path)
	if res.Meta == nil {
		res.Meta = map[string]any{}
	}
	res.Meta["offload"] = path
	return ga.Next()
}

func (h *Hook) write(name, content string) (string, error) {
	sum := sha256.Sum256([]byte(name + "\x00" + content))
	file := safeName(name) + "-" + hex.EncodeToString(sum[:6]) + ".txt"
	dir := filepath.Join(h.opts.Dir, time.Now().Format("20060102"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(dir, file))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func safeName(name string) string {
	if name == "" {
		return "tool"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
