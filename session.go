package ga

import (
	"context"

	"github.com/czasg/go-agent/schema"
)

// Session 是 Agent.Run(ctx, history) 之上的一层便利壳（语法糖）。
//
// 它替你保管跨轮的消息历史：每次 Run 用当前累积的历史，结束后把结果写回。
// 这样多轮对话就不用自己在外面手动拼 history。
//
// 定位说明：Session 不是框架的核心概念，只是给“想快速跑个多轮对话”的场景
// 用的。真正的业务（需要落库、多租户、按 token 截断等）应该直接用无状态的
// Agent.Run(ctx, history)，自己掌控历史的加载与持久化——框架里没有一行 DB
// 代码，持久化通过 AgentEndHook / onEvent 插进来。
type Session struct {
	agent    *Agent
	messages []*schema.Message
}

// NewSession 基于一个 Agent 开启一次会话。可选传入初始历史。
func (a *Agent) NewSession(history ...*schema.Message) *Session {
	msgs := make([]*schema.Message, 0, len(history))
	msgs = append(msgs, history...)
	return &Session{agent: a, messages: msgs}
}

// Messages 返回当前会话累积的完整历史。
func (s *Session) Messages() []*schema.Message { return s.messages }

// AddUserMessage 追加一条用户消息（下一次 Run 时带上）。
func (s *Session) AddUserMessage(content string) {
	s.messages = append(s.messages, &schema.Message{Role: schema.User, Content: content})
}

// Run 用当前历史跑一轮 agent loop，并把产出的完整历史写回会话，
// 供下一轮继续。返回本轮的 RunResult。
func (s *Session) Run(ctx context.Context) (*schema.RunResult, error) {
	r, err := s.agent.Run(ctx, s.messages)
	if r != nil {
		s.messages = r.Messages // 回写：包含本轮新增的 assistant/tool 消息
	}
	return r, err
}
