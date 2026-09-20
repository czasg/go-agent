// tools.go 定义 filesystem 的四个工具，全部经 ga.NewTool 构造：schema 从参数
// 结构体的 tag 反射生成（json 定名与非必填，jsonschema:"description=..." 定
// 描述），不手写 JSON schema。文件读写直接走 os——demo 不引入 fs 抽象，保持
// 与 ext/tool/ask、approve 一样的自包含风格。
package filesystem

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/czasg/go-agent"
)

const readDefaultLines = 2000 // 默认读取行数，防大文件一次撑爆上下文
const readMaxChars = 200_000  // 单次结果字符上限：分页限行不限字节，单行超长（minified）文件需另行设防

type readArgs struct {
	Path   string `json:"path" jsonschema:"description=文件路径"`
	Offset int    `json:"offset,omitempty" jsonschema:"description=起始行号，1 起，默认 1"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=读取行数，默认 2000"`
}

// readTool 按行分页读取：offset/limit 缺省时读前 2000 行；未读完时尾部标注
// 剩余行数与续读 offset，模型据此翻页——大文件对模型不再是只有前半截的黑盒。
// 行数之外另有整段字符上限（readMaxChars）：少数超长行文件（压缩 JS、单行大
// JSON）行数不多但体量巨大，按字符截断兜底。
func readTool(h *Hook) (ga.BaseTool, error) {
	return ga.NewTool("read_file",
		"按行读取文件内容（默认第 1 行起 2000 行，可指定 offset/limit 翻页；单次最多返回 200000 字符，超出截断）",
		func(c *ga.Context, in readArgs) (string, error) {
			if in.Path == "" {
				return "", errors.New("path 不能为空")
			}
			if in.Offset <= 0 {
				in.Offset = 1
			}
			if in.Limit <= 0 {
				in.Limit = readDefaultLines
			}
			data, err := os.ReadFile(h.resolve(in.Path))
			if err != nil {
				return "", err
			}
			lines := splitLines(string(data))
			if len(lines) == 0 {
				return "", nil
			}
			if in.Offset > len(lines) {
				return fmt.Sprintf("offset %d 超出文件总行数 %d", in.Offset, len(lines)), nil
			}
			end := min(in.Offset-1+in.Limit, len(lines))
			out := strings.Join(lines[in.Offset-1:end], "\n")
			if runes := utf8.RuneCountInString(out); runes > readMaxChars {
				return string([]rune(out)[:readMaxChars]) +
					fmt.Sprintf("\n[本次内容共 %d 字符，超出单次 %d 字符上限已截断；如需其余部分请分批读取]", runes, readMaxChars), nil
			}
			if end < len(lines) {
				out += fmt.Sprintf("\n[已读第 %d-%d 行，共 %d 行；继续读取请设 offset=%d]",
					in.Offset, end, len(lines), end+1)
			}
			return out, nil
		})
}

// splitLines 按 \n 切行并剥掉 \r；结尾换行不产生末尾空行。
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
	Path    string `json:"path" jsonschema:"description=文件路径"`
	Content string `json:"content" jsonschema:"description=写入的完整内容"`
}

// writeTool 覆盖写入文件，自动创建父目录。经 per-path 队列串行，避免并发写冲突。
func writeTool(h *Hook) (ga.BaseTool, error) {
	return ga.NewTool("write_file", "写入文件（覆盖），自动创建父目录",
		func(c *ga.Context, in writeArgs) (string, error) {
			if in.Path == "" {
				return "", errors.New("path 不能为空")
			}
			p := h.resolve(in.Path)
			unlock := h.lockPath(p)
			defer unlock()
			if dir := filepath.Dir(p); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return "", err
				}
			}
			if err := os.WriteFile(p, []byte(in.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("已写入 %d 字节到 %s", len(in.Content), in.Path), nil
		})
}

type editArgs struct {
	Path    string `json:"path" jsonschema:"description=文件路径"`
	OldText string `json:"old_text" jsonschema:"description=待替换的原文，必须在文件中存在"`
	NewText string `json:"new_text,omitempty" jsonschema:"description=替换后的新文本，留空即删除 old_text"`
}

// editTool 对现有文件做查找替换（替换全部命中，读改写为一次串行操作）。
// old_text 必须存在于文件中，否则报错让模型自纠。new_text 允许空串（删除语义）。
func editTool(h *Hook) (ga.BaseTool, error) {
	return ga.NewTool("edit_file", "对现有文件做查找替换（替换全部命中）：old_text 必须存在于文件中",
		func(c *ga.Context, in editArgs) (string, error) {
			if in.Path == "" || in.OldText == "" {
				return "", errors.New("path 与 old_text 不能为空")
			}
			p := h.resolve(in.Path)
			unlock := h.lockPath(p)
			defer unlock()
			data, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			n := strings.Count(string(data), in.OldText)
			if n == 0 {
				return "", fmt.Errorf("在 %s 中未找到 old_text", in.Path)
			}
			out := strings.ReplaceAll(string(data), in.OldText, in.NewText)
			if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("在 %s 中替换了 %d 处", in.Path, n), nil
		})
}

type terminalArgs struct {
	Command string `json:"command" jsonschema:"description=完整终端命令，语法须与当前系统一致"`
}

// terminalTool 在系统原生终端执行单条命令：Windows 是 cmd，类 Unix 是 sh。
// workDir 为执行目录（空 = 进程 cwd）。不做 shell 探测与切换——设计立场是
// "模型适配环境"：工具描述与 system 注入都标明当前系统，模型据此书写对应
// 语法。输出为 stdout+stderr 合并；退出码非零时输出与退出码一并作为正常结果
// 返回（不报工具错误）——真实报错是模型自纠的依据，仅无法启动/被取消才是 error。
func terminalTool(h *Hook) (ga.BaseTool, error) {
	return ga.NewTool("terminal", terminalDesc(),
		func(c *ga.Context, in terminalArgs) (string, error) {
			if strings.TrimSpace(in.Command) == "" {
				return "", errors.New("command 不能为空")
			}
			// c 本身实现 context.Context：外部取消能打断命令（见 terminalcmd_*.go
			// 的 Cancel 杀进程树）。执行目录锚定 workspace，与文件工具落点一致。
			cmd := terminalCmd(c, in.Command, h.workspace)
			out, err := cmd.CombinedOutput()
			text := strings.TrimSpace(decodeOutput(out))
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return text + fmt.Sprintf("\n[exit code %d]", exitErr.ExitCode()), nil
			}
			if err != nil {
				return text, fmt.Errorf("执行失败: %w", err)
			}
			return text, nil
		})
}

// decodeOutput 把命令输出尽量归一为 UTF-8。demo 不引入 golang.org/x/text，
// 故不做 GBK 解码：Windows 下 terminalCmd 已预置 chcp 65001，绝大多数现代
// 程序输出即 UTF-8；仍非法的字节剥掉（老程序的 OEM 编码输出不强求还原）。
// 如需简中 GBK 老程序的精确解码，可自行引入 x/text 增强本函数。
func decodeOutput(b []byte) string {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}) // 剥 chcp 重定向可能带的 BOM
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "")
}

// terminalDesc 按当前系统给模型提示对应 shell 语法；GOOS 进程内恒定。
func terminalDesc() string {
	if runtime.GOOS == "windows" {
		return "在系统终端执行命令（当前系统 Windows，cmd 语法：dir、type、findstr、&、&&、|）；" +
			"非零退出码时输出与错误码一并返回，据此修正命令"
	}
	return "在系统终端执行命令（当前系统 " + runtime.GOOS + "，POSIX sh 语法：ls、cat、grep、管道与 &&）；" +
		"非零退出码时输出与错误码一并返回，据此修正命令"
}
