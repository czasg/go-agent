package llm

import (
	"net/http"
	"time"
)

// headerTransport 在每个请求上注入自定义 header。
type headerTransport struct {
	Base    http.RoundTripper
	Headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	return t.Base.RoundTrip(req)
}

// streamTimer 专用于 LLM streaming 的分阶段计时器。
type streamTimer struct {
	start          time.Time
	firstToken     time.Time
	reasoningStart time.Time
	reasoningEnd   time.Time
	contentStart   time.Time
}

// newStreamTimer 创建并开始计时。
func newStreamTimer() *streamTimer {
	return &streamTimer{start: time.Now()}
}

// markFirstToken 记录首 token 时间（幂等）。
func (t *streamTimer) markFirstToken() {
	if t.firstToken.IsZero() {
		t.firstToken = time.Now()
	}
}

// markReasoningStart 记录思考阶段开始（幂等）。
func (t *streamTimer) markReasoningStart() {
	if t.reasoningStart.IsZero() {
		t.reasoningStart = time.Now()
	}
}

// markReasoningEnd 记录思考阶段结束（幂等）。
func (t *streamTimer) markReasoningEnd() {
	if t.reasoningEnd.IsZero() {
		t.reasoningEnd = time.Now()
	}
}

// markContentStart 记录正文阶段开始（幂等）。
func (t *streamTimer) markContentStart() {
	if t.contentStart.IsZero() {
		t.contentStart = time.Now()
	}
}

// totalDuration 返回总耗时。
func (t *streamTimer) totalDuration() time.Duration {
	return time.Since(t.start)
}

// timeToFirstToken 返回首 token 延迟。
func (t *streamTimer) timeToFirstToken() time.Duration {
	if t.firstToken.IsZero() {
		return 0
	}
	return t.firstToken.Sub(t.start)
}

// reasoningDuration 返回思考阶段耗时。
func (t *streamTimer) reasoningDuration() time.Duration {
	if t.reasoningStart.IsZero() {
		return 0
	}
	end := t.reasoningEnd
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(t.reasoningStart)
}

// contentDuration 返回正文阶段耗时。
func (t *streamTimer) contentDuration() time.Duration {
	if t.contentStart.IsZero() {
		return 0
	}
	return time.Since(t.contentStart)
}
