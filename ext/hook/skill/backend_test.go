package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemBackend_List(t *testing.T) {
	ctx := context.Background()

	t.Run("空目录返回空列表", func(t *testing.T) {
		dir := t.TempDir()
		b := NewFilesystemBackend(dir)
		skills, err := b.List(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(skills) != 0 {
			t.Fatalf("want 0, got %d", len(skills))
		}
	})

	t.Run("不存在的目录返回空列表", func(t *testing.T) {
		b := NewFilesystemBackend("/nonexistent/path/that/does/not/exist")
		skills, err := b.List(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(skills) != 0 {
			t.Fatalf("want 0, got %d", len(skills))
		}
	})

	t.Run("无 SKILL.md 的子目录跳过", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "not-a-skill"), 0o755)
		os.WriteFile(filepath.Join(dir, "not-a-skill", "readme.txt"), []byte("hello"), 0o644)
		b := NewFilesystemBackend(dir)
		skills, err := b.List(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(skills) != 0 {
			t.Fatalf("want 0, got %d", len(skills))
		}
	})

	t.Run("根目录的 SKILL.md 被忽略（只扫子目录）", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: root\n---\ncontent"), 0o644)
		b := NewFilesystemBackend(dir)
		skills, err := b.List(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(skills) != 0 {
			t.Fatalf("want 0, got %d", len(skills))
		}
	})

	t.Run("正常的 skill 目录", func(t *testing.T) {
		dir := t.TempDir()
		writeSKILL(t, dir, "pdf", `---
name: pdf-processing
description: "Extract text and tables from PDF"
---
# PDF Processing

Use pdfplumber for extraction.`)

		b := NewFilesystemBackend(dir)
		skills, err := b.List(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(skills) != 1 {
			t.Fatalf("want 1, got %d", len(skills))
		}
		if skills[0].Name != "pdf-processing" {
			t.Fatalf("name: %q", skills[0].Name)
		}
		if skills[0].Description != "Extract text and tables from PDF" {
			t.Fatalf("desc: %q", skills[0].Description)
		}
	})

	t.Run("多个 skill 目录", func(t *testing.T) {
		dir := t.TempDir()
		writeSKILL(t, dir, "alpha", "---\nname: alpha\ndescription: first\n---\ncontent a")
		writeSKILL(t, dir, "beta", "---\nname: beta\ndescription: second\n---\ncontent b")

		b := NewFilesystemBackend(dir)
		skills, err := b.List(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(skills) != 2 {
			t.Fatalf("want 2, got %d", len(skills))
		}
		names := map[string]bool{}
		for _, s := range skills {
			names[s.Name] = true
		}
		if !names["alpha"] || !names["beta"] {
			t.Fatalf("names: %v", names)
		}
	})

	t.Run("frontmatter 无 name 时回落目录名", func(t *testing.T) {
		dir := t.TempDir()
		writeSKILL(t, dir, "my-skill", "---\ndescription: some desc\n---\ncontent")

		b := NewFilesystemBackend(dir)
		skills, err := b.List(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(skills) != 1 {
			t.Fatalf("want 1, got %d", len(skills))
		}
		if skills[0].Name != "my-skill" {
			t.Fatalf("name fallback: %q", skills[0].Name)
		}
		if skills[0].Description != "some desc" {
			t.Fatalf("desc: %q", skills[0].Description)
		}
	})

	t.Run("无 frontmatter 时 name 回落目录名 desc 回落首行", func(t *testing.T) {
		dir := t.TempDir()
		writeSKILL(t, dir, "plain", "# 标题即描述\n正文内容")

		b := NewFilesystemBackend(dir)
		skills, err := b.List(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(skills) != 1 {
			t.Fatalf("want 1, got %d", len(skills))
		}
		if skills[0].Name != "plain" {
			t.Fatalf("name fallback: %q", skills[0].Name)
		}
		if skills[0].Description != "标题即描述" {
			t.Fatalf("desc fallback: %q", skills[0].Description)
		}
	})
}

func TestFilesystemBackend_Get(t *testing.T) {
	ctx := context.Background()

	t.Run("按名称获取 skill", func(t *testing.T) {
		dir := t.TempDir()
		writeSKILL(t, dir, "pdf", `---
name: pdf-processing
description: Extract text
---
Full instructions here.`)

		b := NewFilesystemBackend(dir)
		s, err := b.Get(ctx, "pdf-processing")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if s.Name != "pdf-processing" {
			t.Fatalf("name: %q", s.Name)
		}
		if s.Content != "Full instructions here." {
			t.Fatalf("content: %q", s.Content)
		}
		if !strings.HasSuffix(s.BaseDirectory, filepath.Join(dir, "pdf")) {
			t.Fatalf("baseDir: %q", s.BaseDirectory)
		}
	})

	t.Run("skill 不存在返回错误", func(t *testing.T) {
		dir := t.TempDir()
		b := NewFilesystemBackend(dir)
		_, err := b.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "skill not found") {
			t.Fatalf("err: %v", err)
		}
	})
}

func TestSplitFrontmatter(t *testing.T) {
	t.Run("正常的 frontmatter", func(t *testing.T) {
		fm, content := splitFrontmatter("---\nname: test\ndescription: desc\n---\nBody here")
		if fm["name"] != "test" || fm["description"] != "desc" {
			t.Fatalf("fm: %v", fm)
		}
		if content != "Body here" {
			t.Fatalf("content: %q", content)
		}
	})

	t.Run("无 frontmatter 时原样返回", func(t *testing.T) {
		fm, content := splitFrontmatter("no frontmatter\nbody")
		if fm != nil {
			t.Fatalf("fm should be nil: %v", fm)
		}
		if content != "no frontmatter\nbody" {
			t.Fatalf("content: %q", content)
		}
	})

	t.Run("frontmatter 未闭合时原样返回", func(t *testing.T) {
		fm, content := splitFrontmatter("---\nname: x\nno close")
		if fm != nil {
			t.Fatalf("fm should be nil: %v", fm)
		}
		if !strings.HasPrefix(content, "---") {
			t.Fatalf("content should start with ---: %q", content)
		}
	})

	t.Run("嵌套字段被跳过", func(t *testing.T) {
		fm, _ := splitFrontmatter("---\nname: test\nmetadata:\n  author: me\n  version: \"1.0\"\n---\nbody")
		if fm["name"] != "test" {
			t.Fatalf("fm: %v", fm)
		}
		if _, ok := fm["metadata"]; ok {
			t.Fatal("nested field should be skipped")
		}
	})

	t.Run("值带引号被去除", func(t *testing.T) {
		fm, _ := splitFrontmatter("---\nname: \"quoted\"\n---\nbody")
		if fm["name"] != "quoted" {
			t.Fatalf("name: %q", fm["name"])
		}
	})

	t.Run("CRLF 换行", func(t *testing.T) {
		fm, content := splitFrontmatter("---\r\nname: x\r\ndescription: y\r\n---\r\nbody")
		if fm["name"] != "x" || fm["description"] != "y" {
			t.Fatalf("fm: %v", fm)
		}
		if content != "body" {
			t.Fatalf("content: %q", content)
		}
	})
}

func TestFirstLine(t *testing.T) {
	if d := firstLine("# 标题\n正文"); d != "标题" {
		t.Fatalf("got %q", d)
	}
	if d := firstLine(""); d != "" {
		t.Fatalf("empty: %q", d)
	}
	// 超长截断
	long := strings.Repeat("长", 80)
	if d := firstLine(long); !strings.HasSuffix(d, "…") || len([]rune(d)) != 61 {
		t.Fatalf("long: %q", d)
	}
}

// writeSKILL 在 dir/<sub>/SKILL.md 写入内容。
func writeSKILL(t *testing.T, dir, sub, content string) {
	t.Helper()
	path := filepath.Join(dir, sub)
	os.MkdirAll(path, 0o755)
	if err := os.WriteFile(filepath.Join(path, skillFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}
