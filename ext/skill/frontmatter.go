package skill

import "strings"

type frontmatter struct {
	values   map[string]string
	metadata map[string]string
}

func splitFrontmatter(text string) (frontmatter, string, bool) {
	fm := frontmatter{values: map[string]string{}, metadata: map[string]string{}}
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || strings.TrimSpace(lines[i]) != "---" {
		return frontmatter{values: map[string]string{}, metadata: map[string]string{}}, text, false
	}
	i++

	nested := false
	for i < len(lines) {
		raw := lines[i]
		if strings.TrimSpace(raw) == "---" {
			rest := strings.Join(lines[i+1:], "\n")
			return fm, strings.TrimLeft(rest, "\n"), true
		}
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			i++
			continue
		}
		key, value, ok := cutKV(line)
		if !ok {
			i++
			continue
		}
		if indentOf(raw) > 0 {
			if nested && value != "" {
				fm.metadata[key] = unquote(value)
			}
			i++
			continue
		}
		nested = false
		switch {
		case value == "":
			nested = key == "metadata"
			i++
		case value[0] == '|' || value[0] == '>':
			block, next := readBlock(lines, i+1)
			i = next
			if value[0] == '|' {
				fm.values[key] = strings.Join(block, "\n")
			} else {
				fm.values[key] = fold(block)
			}
		default:
			fm.values[key] = unquote(value)
			i++
		}
	}
	return frontmatter{values: map[string]string{}, metadata: map[string]string{}}, "", false
}

func readBlock(lines []string, start int) ([]string, int) {
	var raw []string
	i := start
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.TrimSpace(line) == "" {
			raw = append(raw, "")
			i++
			continue
		}
		if indentOf(line) == 0 {
			break
		}
		raw = append(raw, line)
		i++
	}

	base := -1
	for _, line := range raw {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if n := indentOf(line); base < 0 || n < base {
			base = n
		}
	}

	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		if len(line) >= base {
			out = append(out, strings.TrimRight(line[base:], " \t"))
		} else {
			out = append(out, "")
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out, i
}

func fold(block []string) string {
	var b strings.Builder
	blank := true
	for _, line := range block {
		if line == "" {
			b.WriteString("\n")
			blank = true
			continue
		}
		if !blank {
			b.WriteString(" ")
		}
		b.WriteString(line)
		blank = false
	}
	return strings.TrimSpace(b.String())
}

func indentOf(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}

func cutKV(line string) (string, string, bool) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return key, "", true
	}
	if value[0] == '"' || value[0] == '\'' {
		return key, unquote(value), true
	}
	return key, value, true
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == s[len(s)-1] && (s[0] == '"' || s[0] == '\'') {
		inner := s[1 : len(s)-1]
		if s[0] == '"' {
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\n`, "\n")
			inner = strings.ReplaceAll(inner, `\\`, `\`)
		}
		return inner
	}
	return s
}
