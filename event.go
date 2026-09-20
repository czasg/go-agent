package ga

import "github.com/czasg/go-agent/schema"

type EventType string

const (
	AgentStartEvent     EventType = "agent_start"     // 仅执行一次
	AgentEndEvent       EventType = "agent_end"       // 仅执行一次
	MessageDeltaEvent   EventType = "message_delta"   // 模型流式输出
	MessageEndEvent     EventType = "message_end"     // 每次调用大模型后执行一次
	ToolStartEvent      EventType = "tool_start"      // 每次调用工具前执行一次
	ToolEndEvent        EventType = "tool_end"        // 每次调用工具后执行一次
	IterationStartEvent EventType = "iteration_start" // 每轮迭代开始（每轮模型调用前）
	IterationEndEvent   EventType = "iteration_end"   // 每轮迭代结束（工具执行完、下一轮模型调用前）
)

// Event 是引擎在生命周期点主动发出的观察事件。
// 与 hook（切面，管"拦截/改状态"）平级：hook 改变执行，Event 只观察执行。
// 消费方据此落库、渲染、记录日志，不应在回调里修改执行状态。
type Event struct {
	Type      EventType
	Iteration int    // 当前迭代号（AgentStart 为 0）
	CallID    string // 工具调用透传的标识（如 task 子循环），主循环为空串
	Data      any    // 事件负载，见各事件常量注释
}

// OnEvent 观察回调。引擎当前串行调用；实现应快速返回，
// 避免阻塞 loop。需要异步消费时自行起 goroutine 或缓冲 channel。
type OnEvent func(e Event)

// EventHandler 是 OnEvent 的便捷适配层。
// 使用方只需按需设置感兴趣的 Handler 字段，无需自行编写 switch 分支和 Data 类型断言。
// OnEvent() 会将统一的 Event 分发到对应字段，未设置的字段自动跳过。
type EventHandler struct {
	OnAgentStartHandler     func()                           // 对应 AgentStartEvent
	OnAgentEndHandler       func(reason schema.StopReason)   // 对应 AgentEndEvent
	OnMessageDeltaHandler   func(delta *schema.MessageDelta) // 对应 MessageDeltaEvent
	OnMessageEndHandler     func(msg *schema.Message)        // 对应 MessageEndEvent
	OnToolStartHandler      func(call *schema.ToolCall)      // 对应 ToolStartEvent
	OnToolEndHandler        func(call *schema.ToolCall)      // 对应 ToolEndEvent
	OnIterationStartHandler func(iteration int)              // 对应 IterationStartEvent
	OnIterationEndHandler   func(iteration int)              // 对应 IterationEndEvent
}

func (e EventHandler) OnEvent() OnEvent {
	return func(event Event) {
		switch event.Type {
		case AgentStartEvent:
			if e.OnAgentStartHandler != nil {
				e.OnAgentStartHandler()
			}
		case AgentEndEvent:
			if e.OnAgentEndHandler != nil {
				if reason, ok := event.Data.(schema.StopReason); ok {
					e.OnAgentEndHandler(reason)
				}
			}
		case MessageDeltaEvent:
			if e.OnMessageDeltaHandler != nil {
				if delta, ok := event.Data.(*schema.MessageDelta); ok {
					e.OnMessageDeltaHandler(delta)
				}
			}
		case MessageEndEvent:
			if e.OnMessageEndHandler != nil {
				if msg, ok := event.Data.(*schema.Message); ok {
					e.OnMessageEndHandler(msg)
				}
			}
		case ToolStartEvent:
			if e.OnToolStartHandler != nil {
				if call, ok := event.Data.(*schema.ToolCall); ok {
					e.OnToolStartHandler(call)
				}
			}
		case ToolEndEvent:
			if e.OnToolEndHandler != nil {
				if call, ok := event.Data.(*schema.ToolCall); ok {
					e.OnToolEndHandler(call)
				}
			}
		case IterationStartEvent:
			if e.OnIterationStartHandler != nil {
				if iter, ok := event.Data.(int); ok {
					e.OnIterationStartHandler(iter)
				}
			}
		case IterationEndEvent:
			if e.OnIterationEndHandler != nil {
				if iter, ok := event.Data.(int); ok {
					e.OnIterationEndHandler(iter)
				}
			}
		}
	}
}

func (e EventHandler) OnEventChannel() (chan Event, OnEvent) {
	ch := make(chan Event, 256)
	onEvent := e.OnEvent()
	return ch, func(e Event) {
		ch <- e
		if e.Type == AgentEndEvent {
			close(ch)
		}
		onEvent(e)
	}
}
