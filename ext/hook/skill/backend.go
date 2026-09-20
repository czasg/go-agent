package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const skillFileName = "SKILL.md"

// FilesystemBackend 从文件系统加载技能。
// 扫描 baseDir 下一级子目录中含 SKILL.md 的目录作为技能。
type FilesystemBackend struct {
	baseDir string
}

// NewFilesystemBackend 创建文件系统 Backend。
// baseDir 是技能目录的根路径，每个子目录（含 SKILL.md）是一个技能。
func NewFilesystemBackend(baseDir string) *FilesystemBackend {
	return &FilesystemBackend{baseDir: baseDir}
}

// List 扫描 baseDir 下所有含 SKILL.md 的子目录，只解析 frontmatter 返回元数据。
func (b *FilesystemBackend) List(ctx context.Context) ([]FrontMatter, error) {
	skills, err := b.loadAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FrontMatter, 0, len(skills))
	for _, s := range skills {
		out = append(out, s.FrontMatter)
	}
	return out, nil
}

// Get 按名称加载完整技能（含指令正文）。
func (b *FilesystemBackend) Get(ctx context.Context, name string) (Skill, error) {
	skills, err := b.loadAll(ctx)
	if err != nil {
		return Skill{}, err
	}
	for _, s := range skills {
		if s.Name == name {
			return s, nil
		}
	}
	return Skill{}, fmt.Errorf("skill not found: %s", name)
}

// loadAll 扫描并加载所有技能。
func (b *FilesystemBackend) loadAll(_ context.Context) ([]Skill, error) {
	entries, err := os.ReadDir(b.baseDir)
	if err != nil {
		// 目录不可访问视为无技能（可选目录），不报错。
		return nil, nil
	}

	var skills []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(b.baseDir, e.Name(), skillFileName)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			continue // 无 SKILL.md 的目录不是技能
		}
		fm, content := splitFrontmatter(string(data))
		name := strings.TrimSpace(fm["name"])
		if name == "" {
			name = e.Name() // 容错：frontmatter 无 name 时回落目录名
		}
		desc := strings.TrimSpace(fm["description"])
		if desc == "" {
			desc = firstLine(content) // 容错：回落正文首行
		}
		skills = append(skills, Skill{
			FrontMatter: FrontMatter{
				Name:        name,
				Description: desc,
			},
			Content:       strings.TrimSpace(content),
			BaseDirectory: filepath.Join(b.baseDir, e.Name()),
		})
	}
	return skills, nil
}

// ── Frontmatter 解析 ──────────────────────────────────────────

// splitFrontmatter 剥离 YAML frontmatter（--- 到 ---），只取顶层扁平字段。
// 不引入 YAML 依赖——规范必填字段都是扁平标量。无 frontmatter 时原样返回。
func splitFrontmatter(body string) (map[string]string, string) {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, body
	}
	meta := map[string]string{}
	for i := 1; i < len(lines); i++ {
		raw := lines[i]
		line := strings.TrimSpace(raw)
		if line == "---" {
			rest := strings.Join(lines[i+1:], "\n")
			rest = strings.TrimPrefix(rest, "\n")
			return meta, rest
		}
		// 跳过缩进行（嵌套块的子项，如 metadata.author）
		if raw != strings.TrimLeft(raw, " \t") {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			k = strings.TrimSpace(k)
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if k != "" && v != "" {
				meta[k] = v
			}
		}
	}
	return nil, body // frontmatter 未闭合，视为普通正文
}

// firstLine 取正文首个非空行（# 标题去前缀），截 60 rune。
func firstLine(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, "#")), "#"))
			r := []rune(line)
			if len(r) > 60 {
				return string(r[:60]) + "…"
			}
			return line
		}
	}
	return ""
}
