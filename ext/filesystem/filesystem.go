package filesystem

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ga "github.com/czasg/go-agent"
)

type FS struct {
	workspace string
	tools     []*ga.Tool
	hint      string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

type Option func(*FS)

func WithDir(dir string) Option {
	return func(f *FS) { f.workspace = dir }
}

func New(opts ...Option) *FS {
	f := &FS{locks: map[string]*sync.Mutex{}}
	for _, o := range opts {
		if o != nil {
			o(f)
		}
	}
	if f.workspace != "" {
		_ = os.MkdirAll(f.workspace, 0o755)
	}
	f.hint = buildHint(f.workspace)
	f.tools = []*ga.Tool{
		readTool(f),
		writeTool(f),
		editTool(f),
		terminalTool(f),
	}
	return f
}

func (f *FS) Tools() []*ga.Tool {
	return append([]*ga.Tool(nil), f.tools...)
}

func (f *FS) Options() []ga.Option {
	return []ga.Option{
		ga.WithTools(f.tools...),
		ga.WithHooks(f),
	}
}

func (f *FS) Install(a *ga.Agent) {
	a.Tools.Add(f.tools...)
	a.BindHooks(f)
}

func (f *FS) OnRunStart(c *ga.Context) {
	c.SetSystemSection("env", f.hint)
}

func buildHint(workspace string) string {
	hint := "# 运行环境\n" +
		"操作系统：" + runtime.GOOS +
		"（terminal 用系统原生 shell：Windows 写 cmd 语法 dir/type/findstr，类 Unix 写 POSIX 语法 ls/cat/grep）"
	if workspace != "" {
		hint += "\n工作区：" + workspace + "（文件工具的相对路径以此为基准，绝对路径原样使用）"
	}
	return hint
}

func (f *FS) resolve(path string) string {
	if f.workspace == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(f.workspace, path)
}

func (f *FS) lockPath(path string) func() {
	f.mu.Lock()
	l, ok := f.locks[path]
	if !ok {
		l = &sync.Mutex{}
		f.locks[path] = l
	}
	f.mu.Unlock()
	l.Lock()
	return l.Unlock
}
