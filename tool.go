package ga

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

type ToolHandler func(c *Context, args json.RawMessage) *ToolResult

type ToolHandlerMiddleware func(next ToolHandler) ToolHandler

type Tool struct {
	Name        string
	Description string
	Params      map[string]any
	Handler     ToolHandler
	Serial      bool

	lock *sync.Mutex
}

func UseTools(tools []*Tool, mws ...ToolHandlerMiddleware) []*Tool {
	out := make([]*Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Use(mws...))
	}
	return out
}

func (t *Tool) mutex() *sync.Mutex {
	if t.lock == nil {
		t.lock = &sync.Mutex{}
	}
	return t.lock
}

func (t *Tool) Wrap(handler ToolHandler) *Tool {
	if t == nil {
		return nil
	}
	out := *t
	out.Handler = handler
	out.lock = nil
	return &out
}

func (t *Tool) Use(mws ...ToolHandlerMiddleware) *Tool {
	if t == nil {
		return nil
	}
	next := t.Handler
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] == nil {
			continue
		}
		next = mws[i](next)
	}
	return t.Wrap(next)
}

type ToolResult struct {
	Content      string
	MultiContent []Part
	IsError      bool
	Stop         bool
	Meta         map[string]any
}

func Result(v any) *ToolResult {
	switch t := v.(type) {
	case nil:
		return &ToolResult{Content: "(no output)"}
	case *ToolResult:
		if t == nil {
			return &ToolResult{Content: "(no output)"}
		}
		return t
	case ToolResult:
		r := t
		return &r
	case string:
		if t == "" {
			return &ToolResult{Content: "(no output)"}
		}
		return &ToolResult{Content: t}
	case []Part:
		if len(t) == 0 {
			return &ToolResult{Content: "(no output)"}
		}
		return &ToolResult{MultiContent: t}
	case []byte:
		if len(t) == 0 {
			return &ToolResult{Content: "(no output)"}
		}
		return &ToolResult{Content: string(t)}
	case error:
		return &ToolResult{Content: t.Error(), IsError: true}
	}

	b, err := json.Marshal(v)
	if err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}
	}
	if len(b) == 0 || string(b) == "null" || string(b) == `""` {
		return &ToolResult{Content: "(no output)"}
	}
	return &ToolResult{Content: string(b)}
}

func Err(err error) *ToolResult {
	if err == nil {
		return &ToolResult{Content: "(no output)"}
	}
	return &ToolResult{Content: err.Error(), IsError: true}
}

func (r *ToolResult) AsErr() *ToolResult {
	if r == nil {
		r = &ToolResult{}
	}
	r.IsError = true
	return r
}

func (r *ToolResult) AsStop() *ToolResult {
	if r == nil {
		r = &ToolResult{}
	}
	r.Stop = true
	return r
}

func (r *ToolResult) WithMeta(k string, v any) *ToolResult {
	if r == nil {
		r = &ToolResult{}
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	r.Meta[k] = v
	return r
}

func (r *ToolResult) Empty() bool {
	if r == nil {
		return true
	}
	return r.Content == "" && len(r.MultiContent) == 0
}

type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolBox struct {
	mu    sync.RWMutex
	tools map[string]*Tool
	order []string
	allow map[string]struct{}
	deny  map[string]struct{}
}

func NewToolBox(tools ...*Tool) *ToolBox {
	b := &ToolBox{
		tools: map[string]*Tool{},
		allow: map[string]struct{}{},
		deny:  map[string]struct{}{},
	}
	b.Add(tools...)
	return b
}

func (b *ToolBox) Add(tools ...*Tool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range tools {
		if t == nil || t.Name == "" {
			continue
		}
		if t.Serial && t.lock == nil {
			t.lock = &sync.Mutex{}
		}
		if _, ok := b.tools[t.Name]; !ok {
			b.order = append(b.order, t.Name)
		}
		b.tools[t.Name] = t
	}
}

func (b *ToolBox) Remove(names ...string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range names {
		if _, ok := b.tools[n]; !ok {
			continue
		}
		delete(b.tools, n)
		for i, name := range b.order {
			if name == n {
				b.order = append(b.order[:i], b.order[i+1:]...)
				break
			}
		}
	}
}

func (b *ToolBox) Replace(t *Tool) {
	if b == nil || t == nil || t.Name == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.tools[t.Name]; !ok {
		b.order = append(b.order, t.Name)
	}
	b.tools[t.Name] = t
}

func (b *ToolBox) Get(name string) (*Tool, bool) {
	if b == nil {
		return nil, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	t, ok := b.tools[name]
	return t, ok
}

func (b *ToolBox) List() []*Tool {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]*Tool, 0, len(b.order))
	for _, n := range b.order {
		if t, ok := b.tools[n]; ok {
			out = append(out, t)
		}
	}
	return out
}

func (b *ToolBox) Names() []string {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]string(nil), b.order...)
}

func (b *ToolBox) Clone() *ToolBox {
	if b == nil {
		return NewToolBox()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := NewToolBox()
	for _, n := range b.order {
		if t, ok := b.tools[n]; ok {
			cp := *t
			out.Add(&cp)
		}
	}
	out.allow = make(map[string]struct{}, len(b.allow))
	for k := range b.allow {
		out.allow[k] = struct{}{}
	}
	out.deny = make(map[string]struct{}, len(b.deny))
	for k := range b.deny {
		out.deny[k] = struct{}{}
	}
	return out
}

func (b *ToolBox) Allow(names ...string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.allow == nil {
		b.allow = map[string]struct{}{}
	}
	for _, n := range names {
		b.allow[n] = struct{}{}
	}
}

func (b *ToolBox) Deny(names ...string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.deny == nil {
		b.deny = map[string]struct{}{}
	}
	for _, n := range names {
		b.deny[n] = struct{}{}
	}
}

func (b *ToolBox) Unallow(names ...string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range names {
		delete(b.allow, n)
	}
}

func (b *ToolBox) Undeny(names ...string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range names {
		delete(b.deny, n)
	}
}

func (b *ToolBox) Reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allow = map[string]struct{}{}
	b.deny = map[string]struct{}{}
}

func (b *ToolBox) visibleLocked() []*Tool {
	out := make([]*Tool, 0, len(b.order))
	for _, n := range b.order {
		if len(b.allow) > 0 {
			if _, ok := b.allow[n]; !ok {
				continue
			}
		}
		if _, ok := b.deny[n]; ok {
			continue
		}
		if t, ok := b.tools[n]; ok {
			out = append(out, t)
		}
	}
	return out
}

func (b *ToolBox) Visible() []*Tool {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.visibleLocked()
}

func (b *ToolBox) Specs() []ToolSpec {
	tools := b.Visible()
	out := make([]ToolSpec, 0, len(tools))
	for _, t := range tools {
		params := t.Params
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, ToolSpec{Name: t.Name, Description: t.Description, Parameters: params})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (b *ToolBox) EnableMatching(q string) []string {
	if b == nil {
		return nil
	}
	q = strings.ToLower(strings.TrimSpace(q))
	b.mu.Lock()
	defer b.mu.Unlock()
	var hits []string
	for _, n := range b.order {
		t, ok := b.tools[n]
		if !ok {
			continue
		}
		if q == "" || strings.Contains(strings.ToLower(t.Name), q) || strings.Contains(strings.ToLower(t.Description), q) {
			hits = append(hits, n)
		}
	}
	if len(b.allow) == 0 {
		for _, n := range hits {
			delete(b.deny, n)
		}
	} else {
		for _, n := range hits {
			b.allow[n] = struct{}{}
		}
	}
	return hits
}

func safeCall(t *Tool, c *Context, args json.RawMessage) (res *ToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = &ToolPanic{Tool: t.Name, Value: r}
		}
	}()
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	return t.Handler(c, args), nil
}

type ToolPanic struct {
	Tool  string
	Value any
}

func (e *ToolPanic) Error() string {
	return "tool " + e.Tool + " panicked: " + stringify(e.Value)
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "unknown"
	}
	return string(b)
}
