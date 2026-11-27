package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	ga "github.com/czasg/go-agent"
)

const (
	defaultToolName = "skill"
	defaultMaxBody  = 20000
	skillFileName   = "SKILL.md"
	maxNameLen      = 64
	maxDescLen      = 1024
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Skill struct {
	Name          string
	Description   string
	Content       string
	BaseDirectory string
	License       string
	Compatibility string
	AllowedTools  []string
	Metadata      map[string]string
	Truncated     bool
}

type source struct {
	dir   string
	file  string
	skill *Skill
}

type Option func(*Registry)

func WithDir(dir string) Option {
	return func(r *Registry) {
		if dir = strings.TrimSpace(dir); dir != "" {
			r.sources = append(r.sources, source{dir: dir})
		}
	}
}

func WithFile(path string) Option {
	return func(r *Registry) {
		if path = strings.TrimSpace(path); path != "" {
			r.sources = append(r.sources, source{file: path})
		}
	}
}

func WithSkill(s Skill) Option {
	return func(r *Registry) {
		r.sources = append(r.sources, source{skill: &s})
	}
}

func WithPrompt(prompt string) Option {
	return func(r *Registry) { r.prompt = prompt }
}

func WithToolName(name string) Option {
	return func(r *Registry) {
		if name = strings.TrimSpace(name); name != "" {
			r.toolName = name
		}
	}
}

func WithMaxBody(n int) Option {
	return func(r *Registry) {
		if n > 0 {
			r.maxBody = n
		}
	}
}

type Registry struct {
	mu       sync.RWMutex
	skills   map[string]Skill
	names    []string
	errs     []error
	block    string
	sources  []source
	tool     *ga.Tool
	prompt   string
	toolName string
	maxBody  int
}

func New(opts ...Option) *Registry {
	r := &Registry{skills: map[string]Skill{}, maxBody: defaultMaxBody, toolName: defaultToolName}
	for _, o := range opts {
		if o != nil {
			o(r)
		}
	}
	if r.maxBody <= 0 {
		r.maxBody = defaultMaxBody
	}
	if r.toolName == "" {
		r.toolName = defaultToolName
	}
	r.Reindex()
	r.tool = r.buildTool()
	return r
}

func (r *Registry) Reindex() {
	skills := map[string]Skill{}
	origins := map[string]string{}
	var errs []error

	add := func(s Skill, origin string) {
		if err := validate(&s, r.maxBody); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", origin, err))
			return
		}
		if prev, ok := origins[s.Name]; ok {
			errs = append(errs, fmt.Errorf("%s: 技能名 %q 与 %s 重复，保留先加载的", origin, s.Name, prev))
			return
		}
		origins[s.Name] = origin
		skills[s.Name] = s
	}

	for _, src := range r.sources {
		switch {
		case src.skill != nil:
			origin := "WithSkill"
			if src.skill.Name != "" {
				origin = "WithSkill(" + src.skill.Name + ")"
			}
			add(*src.skill, origin)
		case src.file != "":
			r.loadPath(src.file, "", add, &errs)
		case src.dir != "":
			r.loadDir(src.dir, add, &errs)
		}
	}

	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	sort.Strings(names)

	block := ""
	if len(names) > 0 {
		block = renderPrompt(r.prompt, r.toolName, renderIndex(names, skills))
	}

	r.mu.Lock()
	r.skills = skills
	r.names = names
	r.errs = errs
	r.block = block
	r.mu.Unlock()
}

func (r *Registry) loadDir(dir string, add func(Skill, string), errs *[]error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			*errs = append(*errs, fmt.Errorf("扫描技能目录 %s: %w", dir, err))
		}
		return
	}
	if isFile(filepath.Join(dir, skillFileName)) {
		r.loadPath(filepath.Join(dir, skillFileName), filepath.Base(filepath.Clean(dir)), add, errs)
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name(), skillFileName)
		if !isFile(path) {
			continue
		}
		r.loadPath(path, e.Name(), add, errs)
	}
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (r *Registry) loadPath(path, dirName string, add func(Skill, string), errs *[]error) {
	data, err := os.ReadFile(path)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("读取技能文件 %s: %w", path, err))
		return
	}
	s, err := parse(string(data), filepath.Dir(path), dirName, r.maxBody)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", path, err))
		return
	}
	add(*s, path)
}

func parse(text, baseDir, dirName string, maxBody int) (*Skill, error) {
	fm, body, ok := splitFrontmatter(text)
	if !ok {
		return nil, fmt.Errorf("缺少合法的 YAML frontmatter（文件须以 --- 开头并以 --- 结束）")
	}
	s := &Skill{
		Name:          strings.TrimSpace(fm.values["name"]),
		Description:   strings.TrimSpace(fm.values["description"]),
		Content:       body,
		BaseDirectory: baseDir,
		License:       strings.TrimSpace(fm.values["license"]),
		Compatibility: strings.TrimSpace(fm.values["compatibility"]),
		AllowedTools:  strings.Fields(fm.values["allowed-tools"]),
		Metadata:      fm.metadata,
	}
	if err := validate(s, maxBody); err != nil {
		return nil, err
	}
	if dirName != "" && s.Name != dirName {
		return nil, fmt.Errorf("name %q 与目录名 %q 不一致", s.Name, dirName)
	}
	return s, nil
}

func validate(s *Skill, maxBody int) error {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return fmt.Errorf("frontmatter 缺少 name")
	}
	if !namePattern.MatchString(name) || utf8.RuneCountInString(name) > maxNameLen {
		return fmt.Errorf("name %q 非法：须为 1-%d 字符的小写字母、数字与连字符", name, maxNameLen)
	}
	desc := strings.TrimSpace(s.Description)
	if desc == "" {
		return fmt.Errorf("技能 %s 缺少 description", name)
	}
	if n := utf8.RuneCountInString(desc); n > maxDescLen {
		return fmt.Errorf("技能 %s 的 description 有 %d 字符，超过 %d 字符上限", name, n, maxDescLen)
	}

	s.Name = name
	s.Description = desc
	s.Content = strings.TrimSpace(s.Content)
	s.AllowedTools = append([]string(nil), s.AllowedTools...)
	if s.Metadata == nil {
		s.Metadata = map[string]string{}
	}
	if maxBody > 0 {
		runes := []rune(s.Content)
		if len(runes) > maxBody {
			s.Content = strings.TrimSpace(string(runes[:maxBody]))
			s.Truncated = true
		}
	}
	return nil
}

func (r *Registry) List() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Skill, 0, len(r.names))
	for _, name := range r.names {
		out = append(out, cloneSkill(r.skills[name]))
	}
	return out
}

func (r *Registry) Get(name string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[strings.TrimSpace(name)]
	if !ok {
		return Skill{}, false
	}
	return cloneSkill(s), true
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.names...)
}

func (r *Registry) Errors() []error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]error(nil), r.errs...)
}

func cloneSkill(s Skill) Skill {
	s.AllowedTools = append([]string(nil), s.AllowedTools...)
	md := make(map[string]string, len(s.Metadata))
	for k, v := range s.Metadata {
		md[k] = v
	}
	s.Metadata = md
	return s
}

func (r *Registry) Tool() *ga.Tool { return r.tool }

func (r *Registry) Tools() []*ga.Tool { return []*ga.Tool{r.tool} }

func (r *Registry) Options() []ga.Option {
	return []ga.Option{
		ga.WithTools(r.tool),
		ga.WithHooks(r),
	}
}

func (r *Registry) Install(a *ga.Agent) {
	if a == nil {
		return
	}
	a.Tools.Add(r.tool)
	a.BindHooks(r)
}

func (r *Registry) OnRunStart(c *ga.Context) {
	if c == nil {
		return
	}
	r.mu.RLock()
	block := r.block
	r.mu.RUnlock()
	c.SetSystemSection("skill", block)
}
