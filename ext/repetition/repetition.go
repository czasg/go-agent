package repetition

import (
	"context"
	"fmt"

	ga "github.com/czasg/go-agent"
)

type Options struct {
	MaxPeriod int
	MinPeriod int
	Threshold int
}

type Option func(*Options)

func WithMaxPeriod(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.MaxPeriod = n
		}
	}
}

func WithMinPeriod(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.MinPeriod = n
		}
	}
}

func WithThreshold(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.Threshold = n
		}
	}
}

type Error struct {
	Pattern string
	Period  int
	Repeat  int
	Chars   int
}

func (e *Error) Error() string {
	return fmt.Sprintf("repetition: thinking 连续重复 %d 次 (period %d, pattern %q, buffer %d chars)", e.Repeat, e.Period, e.Pattern, e.Chars)
}

func (e *Error) Retryable() bool { return false }

func Model(opts ...Option) ga.ModelMiddleware {
	o := defaults(opts)
	return func(m ga.Model) ga.Model {
		sm, ok := m.(ga.StreamModel)
		if !ok {
			return m
		}
		return &streamModel{StreamModel: sm, opts: o}
	}
}

func New(m ga.Model, opts ...Option) ga.Model {
	return Model(opts...)(m)
}

func defaults(opts []Option) Options {
	o := Options{MaxPeriod: 200, MinPeriod: 8, Threshold: 3}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	if o.MaxPeriod < 1 {
		o.MaxPeriod = 200
	}
	if o.MinPeriod < 1 {
		o.MinPeriod = 8
	}
	if o.Threshold < 1 {
		o.Threshold = 3
	}
	if o.MinPeriod > o.MaxPeriod {
		o.MinPeriod = o.MaxPeriod
	}
	return o
}

type streamModel struct {
	ga.StreamModel
	opts Options
}

func (m *streamModel) Stream(ctx context.Context, req *ga.ModelRequest) (ga.ModelStream, error) {
	st, err := m.StreamModel.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	return &checkedStream{inner: st, d: newDetector(m.opts)}, nil
}

type checkedStream struct {
	inner ga.ModelStream
	d     *detector
}

func (s *checkedStream) Recv() (*ga.StreamChunk, error) {
	chunk, err := s.inner.Recv()
	if err != nil || chunk == nil {
		return chunk, err
	}
	if e := s.d.check(chunk.ThinkingDelta); e != nil {
		return nil, e
	}
	return chunk, nil
}

func (s *checkedStream) Close() error { return s.inner.Close() }

type detector struct {
	maxPeriod int
	minPeriod int
	threshold int
	buf       []rune
}

func newDetector(o Options) *detector {
	return &detector{maxPeriod: o.MaxPeriod, minPeriod: o.MinPeriod, threshold: o.Threshold}
}

func (d *detector) check(s string) error {
	if s == "" {
		return nil
	}
	d.buf = append(d.buf, []rune(s)...)
	d.trim()
	p, rep := d.period()
	if p == 0 {
		return nil
	}
	return &Error{Pattern: d.display(p), Period: p, Repeat: rep, Chars: len(d.buf)}
}

func (d *detector) trim() {
	maxLen := d.maxPeriod * d.threshold
	if len(d.buf) <= maxLen {
		return
	}
	tail := make([]rune, maxLen)
	copy(tail, d.buf[len(d.buf)-maxLen:])
	d.buf = tail
}

func (d *detector) period() (int, int) {
	limit := d.maxPeriod
	if limit > len(d.buf)/d.threshold {
		limit = len(d.buf) / d.threshold
	}
	for p := 1; p <= limit; p++ {
		rep := tailRepeat(d.buf, p)
		if rep < d.threshold {
			continue
		}
		if p < d.minPeriod {
			return 0, 0
		}
		return p, rep
	}
	return 0, 0
}

func (d *detector) display(p int) string {
	tail := d.buf[len(d.buf)-p:]
	if len(tail) > 80 {
		tail = append(append([]rune(nil), tail[:80]...), []rune("...")...)
	}
	return string(tail)
}

func tailRepeat(buf []rune, p int) int {
	n := len(buf)
	matched := 0
	for i := n - 1; i-p >= 0 && buf[i] == buf[i-p]; i-- {
		matched++
	}
	return 1 + matched/p
}

var _ ga.StreamModel = (*streamModel)(nil)
var _ ga.ModelStream = (*checkedStream)(nil)
