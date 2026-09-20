package ga

import (
	"fmt"
	"sort"
	"sync"

	"github.com/czasg/go-agent/schema"
)

// BaseTool 是工具的最小接口，也是引擎和 ToolStore 唯一认识的类型。
//
// 关于签名的两个刻意选择：
//   - Info() 不返回 error：schema 是构造期的事（见 NewTool），每轮 loop 都会调
//     Info() 收集工具列表，让它保持纯函数、无错误，调用方才干净。
//   - Execute 传入完整的 *schema.ToolCall：工具能访问 call.ID（用于日志关联、
//     子会话标识等），middleware 和 tool 看到统一的签名，无需适配层。
//     真正的"类型化入参"体验由 NewTool[T] 提供（见下），工具作者从不手写解析。
//   - Execute 的返回值 (string, error) 只表达"这次调用的结果"：
//   - (result, nil) 正常结果；
//   - ("", err)     执行失败——引擎会把它格式化成 "tool error: ..." 回喂模型，
//     loop 继续。error 从不表达"终止 loop"。
//     要终止/挂起整个 loop，请在工具内调用 ctx.Abort()（控制流只走 Context）。
type BaseTool interface {
	Info() schema.ToolInfo
	Execute(ctx *Context, call *schema.ToolCall) (string, error)
}

// ── NewTool：类型化工具的泛型适配器 ─────────────────────────────
//
// 这是工具作者面向的入口。它一次性解决了两件重复劳动：
//   1. 用反射从入参结构体 T 生成 JSON Schema（复用 schema.InferToolInfo）；
//   2. 在执行时把原始 args JSON 反序列化成 T，业务函数直接拿类型化结构体。
//
// 于是工具本体退化成一个纯函数 func(ctx, in T) (string, error)，
// 既没有 Info() 样板，也没有手写 json.Unmarshal，更没有 fail() 那种错误样板。

// ToolFunc 是类型化工具的业务函数签名。in 已经是解析好的结构体。
type ToolFunc[T any] func(ctx *Context, in T) (string, error)

// typedTool 把 ToolFunc[T] 适配成 BaseTool。schema 在构造时反射一次并缓存。
type typedTool[T any] struct {
	info schema.ToolInfo
	fn   ToolFunc[T]
}

// NewTool 用反射从 T 生成 schema，并把业务函数包装成 BaseTool。
// name/desc 是工具对模型暴露的名字与描述；T 的字段 tag 决定参数 schema
// （tag 约定见 schema.Parameters 的文档）。
func NewTool[T any](name, desc string, fn ToolFunc[T]) (BaseTool, error) {
	var zero T
	info, err := schema.InferToolInfo(name, desc, zero)
	if err != nil {
		return nil, fmt.Errorf("ga: 构造工具 %q 失败: %w", name, err)
	}
	return &typedTool[T]{info: info, fn: fn}, nil
}

func (t *typedTool[T]) Info() schema.ToolInfo { return t.info }

func (t *typedTool[T]) Execute(ctx *Context, call *schema.ToolCall) (string, error) {
	var in T
	if err := call.JSON(&in); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	return t.fn(ctx, in)
}

// ── ToolStore：工具注册表 ────────────────────────────────────
//
// 两种用法，同一份数据结构：
//   - 构造期：Agent 上持有一份"种子" store（WithTools 内部调 Register）。
//     Agent 是不可变配置，构造完成后不应再改这份种子。
//   - 运行期：每次 Run 在 Context 上持有一份 Copy() 出来的私有副本
//     （见 context.go，字段 c.Tools 直接暴露）。hook/tool 往这份副本
//     Register 动态工具、或用 Allow 收窄可见范围（MCP 热加载、skill
//     携带的工具等），Agent 的种子一个字节都不动——并发复用同一个
//     Agent、Fork 子 agent 全都安全。
//
// 并发：工具是并发执行的（见 agent.go 的 runTools），若允许"工具执行中再
// 注册工具"，就是并发读写同一张 map。故所有方法用 RWMutex 保护：写方法
// （Register/Allow/Deny/ResetWhitelist/ResetBlacklist）走写锁，读方法
// （Get/Names/ListToolInfos）走读锁。这是相比消息历史（只在主循环单
// goroutine 追加，无需锁）多出来的一处——工具的并发模型决定的。
//
// 白名单（可见性，非访问控制）：whitelist 控制 ListToolInfos 对模型暴露哪些
// 工具，用 nil / 非 nil 表达"状态"而非 len==0 / >0，从而三态清晰：
//   - whitelist == nil（默认）    ：关闭过滤，全部工具可见（原有行为，零破坏）
//   - whitelist == 非 nil 空 map  ：开启过滤但未放行任何 → 模型一个工具都看不到
//   - whitelist == {"a": true}    ：开启过滤，只暴露白名单内的工具
//
// 黑名单：blacklist 与 whitelist 同层运作，优先级更高——命中黑名单即排除，
// 即使在白名单内也不例外。典型场景：子 agent 需要排除某个工具（如 task
// 工具排除自身防止递归），用 Deny("task") 一行搞定，无需列举其余所有工具。
// nil = 不排除（默认），非 nil = 排除命中的工具。
//
// 边界：白名单/黑名单只作用于 ListToolInfos（模型"发现"什么），Get 不受影响
// （名单外的工具若被历史里的旧调用或模型幻觉命中，仍可执行）。"禁止执行"
// 是访问控制，属于 middleware 的职责（见 ForTools），不与可见性混在一处。

type ToolStore struct {
	mu        sync.RWMutex
	tools     map[string]BaseTool
	whitelist map[string]bool // nil=关闭全可见；非 nil=只暴露其中的工具
	blacklist map[string]bool // nil=不排除；非 nil=排除命中的工具（优先级高于白名单）
}

func NewToolStore() *ToolStore {
	return &ToolStore{tools: make(map[string]BaseTool)}
}

// Register 注册工具，以 Info().Name 为 key，重复则覆盖。
func (t *ToolStore) Register(tool BaseTool) {
	info := tool.Info()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tools[info.Name] = tool
}

// Get 按名查找。不受白名单影响——可见性 ≠ 访问控制。
func (t *ToolStore) Get(name string) (BaseTool, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	tool, ok := t.tools[name]
	return tool, ok
}

// Allow 把工具名加入白名单（可变参，可批量）。首次调用即开启白名单过滤
// （惰性建 map = 开启），此后 ListToolInfos 只暴露白名单内的工具。
// 空调用 Allow() 也会建出非 nil 空名单 = 开启且全隐藏。
func (t *ToolStore) Allow(names ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.whitelist == nil {
		t.whitelist = make(map[string]bool, len(names))
	}
	for _, n := range names {
		t.whitelist[n] = true
	}
}

// Deny 把工具名加入黑名单（可变参，可批量）。ListToolInfos 过滤时黑名单
// 优先级高于白名单——即使在白名单内，命中黑名单也会被排除。
func (t *ToolStore) Deny(names ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.blacklist == nil {
		t.blacklist = make(map[string]bool, len(names))
	}
	for _, n := range names {
		t.blacklist[n] = true
	}
}

// ResetWhitelist 关闭白名单过滤，恢复"全部可见"（whitelist 置 nil）。
func (t *ToolStore) ResetWhitelist() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.whitelist = nil
}

// ResetBlacklist 清空黑名单，恢复"不排除"（blacklist 置 nil）。
func (t *ToolStore) ResetBlacklist() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blacklist = nil
}

// Copy 返回一份浅拷贝：新 store 持有独立的 map，但复用同一批工具实例
// （工具本体是无状态的构造期产物，可安全共享）。白名单/黑名单一并拷贝：
// nil 拷成 nil、非 nil 深拷一份独立 map——种子上配的可见范围随副本携带，
// 且之后对副本的 Allow/Deny 不写回种子。这是"Agent 种子 → 每次 Run 私有
// 副本"的桥：NewContext 调它，与其对 history 的浅拷贝完全对称。
func (t *ToolStore) Copy() *ToolStore {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m := make(map[string]BaseTool, len(t.tools))
	for k, v := range t.tools {
		m[k] = v
	}
	var wl map[string]bool
	if t.whitelist != nil {
		wl = make(map[string]bool, len(t.whitelist))
		for k, v := range t.whitelist {
			wl[k] = v
		}
	}
	var bl map[string]bool
	if t.blacklist != nil {
		bl = make(map[string]bool, len(t.blacklist))
		for k, v := range t.blacklist {
			bl[k] = v
		}
	}
	return &ToolStore{tools: m, whitelist: wl, blacklist: bl}
}

// Names 返回所有工具名（按名字排序，保证稳定）。不受白名单影响。
func (t *ToolStore) Names() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	names := make([]string, 0, len(t.tools))
	for name := range t.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListToolInfos 收集要暴露给模型的工具 schema（Run 时用）。
// 黑名单优先级高于白名单：命中黑名单即排除，不受白名单影响。
// 白名单开启（whitelist != nil）时只返回名单内的工具，关闭时返回全部。
// 按名字排序返回：工具列表顺序稳定，序列化结果跨请求一致，
// prompt 前缀缓存（KV cache）才能命中。
func (t *ToolStore) ListToolInfos() []schema.ToolInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]schema.ToolInfo, 0, len(t.tools))
	for name, tool := range t.tools {
		if t.blacklist != nil && t.blacklist[name] {
			continue // 黑名单命中：直接排除，优先级最高
		}
		if t.whitelist != nil && !t.whitelist[name] {
			continue // 白名单开启且不在名单内：对模型隐藏
		}
		out = append(out, tool.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
