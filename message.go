package ga

import "github.com/czasg/go-agent/schema"

// FixPlaceholder 是缺失工具结果的占位文案。模型据此得知该调用没有可用结果，
// 而不是把它误读成「执行成功但没有输出」。
const FixPlaceholder = "[tool result missing]"

// MessageStore 是消息历史的便利管理器，类似 ToolStore 之于工具。
//
// 封装了摘要裁剪、轮次切分、按位置插入等常用操作，
// 让 summary hook 和业务层不用手动算下标。
//
// 所有下标操作（Insert/Delete/查找返回的下标）都基于 Messages()（模型可见消息），
// 而非 Raw()（全量原始消息）。这样下标和模型看到的一致，不会错位。
//
// 不加锁：消息历史只在主 loop 的单一 goroutine 里操作（与 hook 一致）。
type MessageStore struct {
	messages []*schema.Message
}

// NewMessageStore 创建 MessageStore。可选传入初始消息。
func NewMessageStore(msgs ...*schema.Message) *MessageStore {
	return &MessageStore{messages: append([]*schema.Message(nil), msgs...)}
}

// Messages 返回模型可见的消息：从最后一条 summary 消息开始，
// 之前的全部丢弃，但始终保留开头的 system 消息。
// 如果没有 summary 消息，返回全部。
func (s *MessageStore) Messages() []*schema.Message {
	idx := s.summaryIndex()
	if idx < 0 {
		return s.messages
	}
	// summary 之前有 system 消息，一起带上。
	if idx > 0 && s.messages[0].Role == schema.System {
		out := make([]*schema.Message, 0, 1+len(s.messages)-idx)
		out = append(out, s.messages[0])
		out = append(out, s.messages[idx:]...)
		return out
	}
	return s.messages[idx:]
}

// summaryIndex 返回最后一条 summary 消息在 s.messages 中的下标，没有返回 -1。
func (s *MessageStore) summaryIndex() int {
	idx := -1
	for i, m := range s.messages {
		if m.Summary {
			idx = i
		}
	}
	return idx
}

// Raw 返回全量原始消息（含摘要消息及被摘要覆盖的旧消息）。
// 用于持久化、事件上报等需要完整历史的场景。
func (s *MessageStore) Raw() []*schema.Message {
	return s.messages
}

// Set 替换全部消息。
func (s *MessageStore) Set(msgs []*schema.Message) {
	s.messages = msgs
}

// AddMessage 追加一条或多条消息。
func (s *MessageStore) AddMessage(msgs ...*schema.Message) {
	s.messages = append(s.messages, msgs...)
}

// AppendSystemHint 往 system 消息追加提示文本。
// 若首条消息是 system 则拼接到其末尾；否则在最前新建一条 system 消息。
// 多次调用可累积，始终保证 system 消息只有一条。
func (s *MessageStore) AppendSystemHint(hint string) {
	if len(s.messages) > 0 && s.messages[0].Role == schema.System {
		s.messages[0].Content += "\n\n" + hint
		return
	}
	s.messages = append([]*schema.Message{{
		Role:    schema.System,
		Content: hint,
	}}, s.messages...)
}

// AddUserMessage 追加一条用户消息。
func (s *MessageStore) AddUserMessage(content string) {
	s.messages = append(s.messages, &schema.Message{Role: schema.User, Content: content})
}

// visibleOffset 返回 Messages() 相对于 s.messages 的起始偏移量。
// Messages() 可能包含 summary 之前的 system 消息（非连续），偏移量需据此调整。
func (s *MessageStore) visibleOffset() int {
	idx := s.summaryIndex()
	if idx < 0 {
		return 0
	}
	// Messages() 包含了 system(0) + summary(idx)..，所以 system 占了 1 个位置，
	// summary 在 Messages() 中的位置是 1，不是 idx。
	if idx > 0 && s.messages[0].Role == schema.System {
		return idx - 1 // system 占 Messages()[0]，summary 占 Messages()[1]，偏移 = idx - 1
	}
	return idx
}

// Insert 在指定位置插入消息（下标基于 Messages()）。
// index == len(Messages()) 表示追加到末尾。
func (s *MessageStore) Insert(index int, msgs ...*schema.Message) {
	rawIdx := s.visibleOffset() + index
	if rawIdx < 0 || rawIdx > len(s.messages) {
		return
	}
	// 必须分配新切片，避免 append 在原数组上覆盖后续元素。
	newMsgs := make([]*schema.Message, 0, len(s.messages)+len(msgs))
	newMsgs = append(newMsgs, s.messages[:rawIdx]...)
	newMsgs = append(newMsgs, msgs...)
	newMsgs = append(newMsgs, s.messages[rawIdx:]...)
	s.messages = newMsgs
}

// Delete 删除 [from, to) 范围的消息（下标基于 Messages()）。
func (s *MessageStore) Delete(from, to int) {
	off := s.visibleOffset()
	rawFrom := off + from
	rawTo := off + to
	if rawFrom < 0 || rawTo > len(s.messages) || rawFrom >= rawTo {
		return
	}
	// 必须分配新切片，避免 append 在原数组上覆盖后续元素。
	newMsgs := make([]*schema.Message, 0, len(s.messages)-(rawTo-rawFrom))
	newMsgs = append(newMsgs, s.messages[:rawFrom]...)
	newMsgs = append(newMsgs, s.messages[rawTo:]...)
	s.messages = newMsgs
}

// ClearTransient 移除所有瞬态消息，每轮开始时调用一次。
func (s *MessageStore) ClearTransient() {
	n := 0
	for _, m := range s.messages {
		if !m.Transient {
			s.messages[n] = m
			n++
		}
	}
	for i := n; i < len(s.messages); i++ {
		s.messages[i] = nil
	}
	s.messages = s.messages[:n]
}

// Fix 双向修理消息历史；历史完整时不做任何修改。
//
// 修补两个方向：
//   - 补缺：assistant 的 tool_call 没有对应 tool 结果 → 补一条占位 tool 消息；
//   - 删孤：tool 结果消息的 ToolCallID 在历史里找不到对应调用 → 删除该消息。
//
// 操作 Raw() 全量消息，而非 Messages()，因为修复需要看到完整历史。
func (s *MessageStore) Fix() {
	s.messages = fixMessages(s.messages)
}

// fixMessages 是底层修复逻辑，操作原始消息切片。
// 历史完整时原样返回（不新建切片）。
func fixMessages(msgs []*schema.Message) []*schema.Message {
	called := make(map[string]bool, len(msgs))
	answered := make(map[string]bool, len(msgs))

	for _, m := range msgs {
		switch m.Role {
		case schema.Assistant:
			for _, c := range m.ToolCalls {
				if c != nil && c.ID != "" {
					called[c.ID] = true
				}
			}
		case schema.Tool:
			if m.ToolCallID != "" {
				answered[m.ToolCallID] = true
			}
		}
	}

	missing, orphan := false, false
	for _, m := range msgs {
		if m.Role == schema.Tool && m.ToolCallID != "" && !called[m.ToolCallID] {
			orphan = true
		}
		if m.Role == schema.Assistant {
			for _, c := range m.ToolCalls {
				if c != nil && c.ID != "" && !answered[c.ID] {
					missing = true
				}
			}
		}
	}
	if !missing && !orphan {
		return msgs
	}

	fixed := make([]*schema.Message, 0, len(msgs)+4)
	var pending []*schema.ToolCall
	flushMissing := func() {
		for _, c := range pending {
			fixed = append(fixed, &schema.Message{
				Role:       schema.Tool,
				Content:    FixPlaceholder,
				ToolCallID: c.ID,
				ToolName:   c.Function.Name,
			})
			answered[c.ID] = true
		}
		pending = nil
	}

	for _, m := range msgs {
		if m.Role == schema.Tool && m.ToolCallID != "" && !called[m.ToolCallID] {
			continue
		}
		if m.Role != schema.Tool && len(pending) > 0 {
			flushMissing()
		}
		if m.Role == schema.Tool && m.ToolCallID != "" {
			for i, c := range pending {
				if c.ID == m.ToolCallID {
					pending = append(pending[:i], pending[i+1:]...)
					break
				}
			}
		}
		fixed = append(fixed, m)
		if m.Role == schema.Assistant {
			for _, c := range m.ToolCalls {
				if c == nil || c.ID == "" || answered[c.ID] {
					continue
				}
				pending = append(pending, c)
			}
		}
	}
	flushMissing()
	return fixed
}

// ── 轮次操作 ─────────────────────────────────────────────────

// RoundRange 表示一轮对话在消息列表中的范围（下标基于 Messages()）。
type RoundRange struct {
	Start    int               // 起始下标（含）
	End      int               // 结束下标（不含）
	Messages []*schema.Message // 本轮消息
}

// Rounds 按 user 消息切分对话轮次（基于 Messages()）。
// 一轮 = 一条 user 消息 + 它后面的所有 assistant/tool 消息，直到下一条 user 消息。
// system 和 summary 消息不参与切分，归入轮次之前的"前缀"。
func (s *MessageStore) Rounds() []RoundRange {
	msgs := s.Messages()
	var rounds []RoundRange
	start := -1

	for i, m := range msgs {
		if m.Role == schema.User && !m.Summary {
			if start >= 0 {
				rounds = append(rounds, RoundRange{
					Start:    start,
					End:      i,
					Messages: msgs[start:i],
				})
			}
			start = i
		}
	}
	if start >= 0 {
		rounds = append(rounds, RoundRange{
			Start:    start,
			End:      len(msgs),
			Messages: msgs[start:],
		})
	}
	return rounds
}

// ExtractBeforeRounds 提取最近 keep 轮之前的所有轮次消息（下标基于 Messages()）。
// 返回提取的轮次范围，业务层可据此做摘要后再 Insert/Delete 回去。
// keep <= 0 或轮次不足时返回 nil。
//
// 典型用法：
//
//	extracted := store.ExtractBeforeRounds(3)
//	if extracted != nil {
//	    summary := summarize(extracted.Messages)
//	    store.Delete(extracted.Start, extracted.End)
//	    store.Insert(extracted.Start, summaryMsg)
//	}
func (s *MessageStore) ExtractBeforeRounds(keep int) *RoundRange {
	all := s.Rounds()
	if keep <= 0 || len(all) <= keep {
		return nil
	}

	firstKeep := all[len(all)-keep]
	msgs := s.Messages()
	extracted := msgs[:firstKeep.Start]

	// 跳过前缀中的 system 和 summary 消息
	start := 0
	for _, m := range extracted {
		if m.Role == schema.System || m.Summary {
			start++
		} else {
			break
		}
	}

	if start >= len(extracted) {
		return nil
	}

	return &RoundRange{
		Start:    start,
		End:      firstKeep.Start,
		Messages: msgs[start:firstKeep.Start],
	}
}

// RoundCount 返回对话轮次总数。
func (s *MessageStore) RoundCount() int {
	return len(s.Rounds())
}

// ── 消息查找 ─────────────────────────────────────────────────

// LastMessageByRole 从后往前查找指定 role 的第一条消息（基于 Messages()）。
// 返回下标与消息。找不到返回 (-1, nil)。
func (s *MessageStore) LastMessageByRole(role schema.RoleType) (int, *schema.Message) {
	msgs := s.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == role {
			return i, msgs[i]
		}
	}
	return -1, nil
}

// LastAssistantMessage 返回最后一条 assistant 消息的下标与消息。
func (s *MessageStore) LastAssistantMessage() (int, *schema.Message) {
	return s.LastMessageByRole(schema.Assistant)
}

// LastUserMessage 返回最后一条 user 消息的下标与消息。
func (s *MessageStore) LastUserMessage() (int, *schema.Message) {
	return s.LastMessageByRole(schema.User)
}

// LastToolMessage 返回最后一条 tool 消息的下标与消息。
func (s *MessageStore) LastToolMessage() (int, *schema.Message) {
	return s.LastMessageByRole(schema.Tool)
}

// ── Token 计算 ────────────────────────────────────────────────

// TotalTokens 返回模型可见消息（Messages()）的 token 总数。
//
// 优先使用模型返回的 Usage.TotalTokens（输入+输出，最准确）；
// 没有则回退到 Message.EstimateTokens() 逐条估算。
func (s *MessageStore) TotalTokens() int {
	msgs := s.Messages()

	// 找最后一条有 Usage 的 assistant 消息，它的 TotalTokens 反映了
	// 上下文窗口的实际占用（输入+输出）。
	idx, last := s.LastAssistantMessage()
	if idx >= 0 && last.Usage.TotalTokens > 0 {
		total := last.Usage.TotalTokens
		// Usage 之后新增的消息（如工具结果）需要补算。
		for _, m := range msgs[idx+1:] {
			total += m.EstimateTokens()
		}
		return total
	}

	// 无 Usage 数据，逐条估算。
	total := 0
	for _, m := range msgs {
		total += m.EstimateTokens()
	}
	return total
}
