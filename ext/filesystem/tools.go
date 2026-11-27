package filesystem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	ga "github.com/czasg/go-agent"
)

const readDefaultLines = 2000
const readMaxChars = 200_000

type readArgs struct {
	Path   string `json:"path" desc:"文件路径" required:"true"`
	Offset int    `json:"offset,omitempty" desc:"起始行号，1 起，默认 1"`
	Limit  int    `json:"limit,omitempty" desc:"读取行数，默认 2000"`
}

func readTool(f *FS) *ga.Tool {
	return ga.NewTool("read_file",
		"按行读取文件内容（默认第 1 行起 2000 行，可指定 offset/limit 翻页；单次最多返回 200000 字符，超出截断）",
		func(c *ga.Context, in readArgs) *ga.ToolResult {
			if in.Path == "" {
				return ga.Result("path 不能为空").AsErr()
			}
			if in.Offset <= 0 {
				in.Offset = 1
			}
			if in.Limit <= 0 {
				in.Limit = readDefaultLines
			}
			data, err := os.ReadFile(f.resolve(in.Path))
			if err != nil {
				return ga.Err(err)
			}
			lines := splitLines(string(data))
			if len(lines) == 0 {
				return ga.Result("")
			}
			if in.Offset > len(lines) {
				return ga.Result(fmt.Sprintf("offset %d 超出文件总行数 %d", in.Offset, len(lines)))
			}
			end := min(in.Offset-1+in.Limit, len(lines))
			out := strings.Join(lines[in.Offset-1:end], "\n")
			if runes := utf8.RuneCountInString(out); runes > readMaxChars {
				return ga.Result(string([]rune(out)[:readMaxChars]) +
					fmt.Sprintf("\n[本次内容共 %d 字符，超出单次 %d 字符上限已截断；如需其余部分请分批读取]", runes, readMaxChars))
			}
			if end < len(lines) {
				out += fmt.Sprintf("\n[已读第 %d-%d 行，共 %d 行；继续读取请设 offset=%d]",
					in.Offset, end, len(lines), end+1)
			}
			return ga.Result(out)
		})
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

type writeArgs struct {
	Path    string `json:"path" desc:"文件路径" required:"true"`
	Content string `json:"content" desc:"写入的完整内容"`
}

func writeTool(f *FS) *ga.Tool {
	return ga.NewTool("write_file", "写入文件（覆盖），自动创建父目录",
		func(c *ga.Context, in writeArgs) *ga.ToolResult {
			if in.Path == "" {
				return ga.Result("path 不能为空").AsErr()
			}
			p := f.resolve(in.Path)
			unlock := f.lockPath(p)
			defer unlock()
			if dir := filepath.Dir(p); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return ga.Err(err)
				}
			}
			if err := os.WriteFile(p, []byte(in.Content), 0o644); err != nil {
				return ga.Err(err)
			}
			return ga.Result(fmt.Sprintf("已写入 %d 字节到 %s", len(in.Content), in.Path))
		})
}

type editArgs struct {
	Path       string `json:"path" desc:"文件路径" required:"true"`
	OldText    string `json:"old_text" desc:"待替换的原文，必须在文件中唯一命中" required:"true"`
	NewText    string `json:"new_text,omitempty" desc:"替换后的新文本，留空即删除 old_text"`
	ReplaceAll bool   `json:"replace_all,omitempty" desc:"old_text 命中多处时是否全部替换，默认 false（多处命中报错）"`
}

func editTool(f *FS) *ga.Tool {
	return ga.NewTool("edit_file", "对现有文件做查找替换：old_text 必须唯一命中；确实要全部替换时显式设 replace_all=true",
		func(c *ga.Context, in editArgs) *ga.ToolResult {
			if in.Path == "" || in.OldText == "" {
				return ga.Result("path 与 old_text 不能为空").AsErr()
			}
			p := f.resolve(in.Path)
			unlock := f.lockPath(p)
			defer unlock()
			data, err := os.ReadFile(p)
			if err != nil {
				return ga.Err(err)
			}
			content := string(data)
			oldText, newText := in.OldText, in.NewText
			crlf := isCRLF(content)
			if crlf {
				content, oldText, newText = toLF(content), toLF(oldText), toLF(newText)
			}
			n := strings.Count(content, oldText)
			if n == 0 {
				return ga.Result(fmt.Sprintf("在 %s 中未找到 old_text", in.Path)).AsErr()
			}
			if n > 1 && !in.ReplaceAll {
				return ga.Result(fmt.Sprintf("在 %s 中命中 %d 处 old_text（第 %s 行），未做修改。请补足上下文使其唯一，或设 replace_all=true 全部替换",
					in.Path, n, hitLines(content, oldText))).AsErr()
			}
			out := strings.ReplaceAll(content, oldText, newText)
			if crlf {
				out = toCRLF(out)
			}
			if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
				return ga.Err(err)
			}
			return ga.Result(fmt.Sprintf("在 %s 中替换了 %d 处", in.Path, n))
		})
}

func isCRLF(s string) bool {
	return strings.Contains(s, "\r\n") && strings.Count(s, "\r\n") == strings.Count(s, "\n")
}

func toLF(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func toCRLF(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func hitLines(content, old string) string {
	var b strings.Builder
	idx, line, scanned, first, last := 0, 0, 0, true, -1
	for {
		i := strings.Index(content[idx:], old)
		if i < 0 {
			return b.String()
		}
		start := idx + i
		line += strings.Count(content[scanned:start], "\n")
		scanned = start
		if line != last {
			if !first {
				b.WriteString("、")
			}
			first = false
			fmt.Fprintf(&b, "%d", line+1)
			last = line
		}
		idx = start + len(old)
	}
}

type terminalArgs struct {
	Command string `json:"command" desc:"完整终端命令，语法须与当前系统一致" required:"true"`
	Timeout int    `json:"timeout,omitempty" desc:"超时秒数，默认 120，最大 3600"`
}

func terminalTool(f *FS) *ga.Tool {
	return ga.NewTool("terminal", terminalDesc(),
		func(c *ga.Context, in terminalArgs) *ga.ToolResult {
			if strings.TrimSpace(in.Command) == "" {
				return ga.Result("command 不能为空").AsErr()
			}
			d := 120 * time.Second
			if in.Timeout > 0 {
				d = time.Duration(in.Timeout) * time.Second
			}
			d = min(max(d, time.Second), 3600*time.Second)
			ctx, cancel := context.WithTimeout(c, d)
			defer cancel()

			cmd := terminalCmd(ctx, in.Command, f.workspace)
			out, err := cmd.CombinedOutput()
			text := strings.TrimSpace(decodeOutput(out))
			if ctx.Err() == context.DeadlineExceeded {
				return ga.Result(fmt.Sprintf("%s\n[命令超时，已终止（上限 %s）；如需更长时间请设 timeout 参数]", text, d)).AsErr()
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return ga.Result(text + fmt.Sprintf("\n[exit code %d]", exitErr.ExitCode()))
			}
			if err != nil {
				return ga.Result(text + fmt.Sprintf("\n执行失败: %v", err)).AsErr()
			}
			return ga.Result(text)
		})
}

func decodeOutput(b []byte) string {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "")
}

func terminalDesc() string {
	if runtime.GOOS == "windows" {
		return "在系统终端执行命令（当前系统 Windows，cmd 语法：dir、type、findstr、&、&&、|）；" +
			"非零退出码时输出与错误码一并返回，据此修正命令"
	}
	return "在系统终端执行命令（当前系统 " + runtime.GOOS + "，POSIX sh 语法：ls、cat、grep、管道与 &&）；" +
		"非零退出码时输出与错误码一并返回，据此修正命令"
}
