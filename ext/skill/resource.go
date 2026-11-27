package skill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	ga "github.com/czasg/go-agent"
)

const (
	maxResourceSize = 2 << 20
	maxListDepth    = 3
	maxListEntries  = 100
)

func (s Skill) resolve(rel string) (string, error) {
	base := strings.TrimSpace(s.BaseDirectory)
	if base == "" {
		return "", fmt.Errorf("技能 %s 是编程式注入的，没有目录，无法读取资源", s.Name)
	}
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("file 不能为空")
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("file 必须是技能目录内的相对路径：%s", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file 不能越出技能目录：%s", rel)
	}
	root, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", fmt.Errorf("技能目录不可用：%w", err)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("技能目录内没有该路径：%s", rel)
	}
	if !within(root, real) {
		return "", fmt.Errorf("file 不能越出技能目录：%s", rel)
	}
	return real, nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func resourceList(base string) string {
	var lines []string
	walk(base, base, 0, &lines)
	return strings.Join(lines, "\n")
}

func walk(root, dir string, depth int, lines *[]string) {
	if depth > maxListDepth || len(*lines) >= maxListEntries {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if depth == 0 && e.Name() == skillFileName {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			*lines = append(*lines, rel+"/")
		} else {
			*lines = append(*lines, fmt.Sprintf("%s (%s)", rel, humanSize(info.Size())))
		}
		if len(*lines) >= maxListEntries {
			*lines = append(*lines, "…（文件较多，未全部列出）")
			return
		}
		if info.IsDir() {
			walk(root, path, depth+1, lines)
			if len(*lines) >= maxListEntries {
				return
			}
		}
	}
}

func (r *Registry) readResource(s Skill, rel string) *ga.ToolResult {
	path, err := s.resolve(rel)
	if err != nil {
		return ga.Result("读取失败：" + err.Error()).AsErr()
	}
	info, err := os.Stat(path)
	if err != nil {
		return ga.Err(err)
	}
	if info.IsDir() {
		entries := listDir(path)
		if len(entries) == 0 {
			return ga.Result(fmt.Sprintf("%s 是空目录", rel))
		}
		return ga.Result(fmt.Sprintf("%s 是目录，包含：\n%s\n\n读取具体文件：%s(name=%q, file=\"%s/<文件名>\")",
			filepath.ToSlash(rel), strings.Join(entries, "\n"), r.toolName, s.Name, strings.TrimSuffix(filepath.ToSlash(rel), "/")))
	}
	if !info.Mode().IsRegular() {
		return ga.Result(fmt.Sprintf("%s 不是普通文件，未载入上下文；路径：%s", rel, path)).AsErr()
	}
	if info.Size() > maxResourceSize {
		return ga.Result(fmt.Sprintf("%s 大小 %s，超过 %s 上限，未载入上下文；需要时用终端或文件工具读取：%s",
			filepath.ToSlash(rel), humanSize(info.Size()), humanSize(maxResourceSize), path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ga.Err(err)
	}
	if isBinary(data) {
		return ga.Result(fmt.Sprintf("%s 是二进制文件（%s），未载入上下文；需要处理时用终端或文件工具，路径：%s",
			filepath.ToSlash(rel), humanSize(info.Size()), path))
	}
	text := strings.TrimSpace(strings.ToValidUTF8(string(data), ""))
	note := ""
	if runes := []rune(text); len(runes) > r.maxBody {
		text = strings.TrimSpace(string(runes[:r.maxBody]))
		note = fmt.Sprintf("\n\n[文件 %s 超过 %d 字符已截断，共 %s；可用 read_file 或终端读取其余部分]",
			filepath.ToSlash(rel), r.maxBody, humanSize(info.Size()))
	}
	if text == "" {
		text = "(空文件)"
	}
	return ga.Result(fmt.Sprintf("# %s（%s）\n%s%s", filepath.ToSlash(rel), humanSize(info.Size()), text, note))
}

func listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if info.IsDir() {
			out = append(out, name+"/")
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s)", name, humanSize(info.Size())))
	}
	return out
}

func isBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
