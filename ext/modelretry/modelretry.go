package modelretry

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	ga "github.com/czasg/go-agent"
)

const EventRetry ga.EventType = "modelretry.retry"

type RetryInfo struct {
	Attempt     int
	MaxAttempts int
	Delay       time.Duration
	Err         error
}

type Options struct {
	MaxAttempts int
	BaseDelay   time.Duration
	RetryIf     func(error) bool
}

func WithMaxAttempts(n int) func(*Options) {
	return func(o *Options) {
		if n > 0 {
			o.MaxAttempts = n
		}
	}
}

func WithBaseDelay(d time.Duration) func(*Options) {
	return func(o *Options) {
		if d > 0 {
			o.BaseDelay = d
		}
	}
}

func WithRetryIf(fn func(error) bool) func(*Options) {
	return func(o *Options) {
		if fn != nil {
			o.RetryIf = fn
		}
	}
}

func Model(opts ...func(*Options)) ga.ModelMiddleware {
	o := Options{MaxAttempts: 3, BaseDelay: 500 * time.Millisecond}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	if o.MaxAttempts < 1 {
		o.MaxAttempts = 1
	}
	if o.BaseDelay <= 0 {
		o.BaseDelay = 500 * time.Millisecond
	}
	if o.RetryIf == nil {
		o.RetryIf = retryable
	}
	return func(m ga.Model) ga.Model {
		r := &retryModel{inner: m, opts: o}
		if sm, ok := m.(ga.StreamModel); ok {
			return &retryStreamModel{retryModel: r, stream: sm}
		}
		return r
	}
}

func New(m ga.Model, opts ...func(*Options)) ga.Model {
	return Model(opts...)(m)
}

type retryableError interface{ Retryable() bool }

func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var r retryableError
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return true
}

type retryModel struct {
	inner ga.Model
	opts  Options
}

func (r *retryModel) Name() string { return r.inner.Name() }

func (r *retryModel) Generate(ctx context.Context, req *ga.ModelRequest) (*ga.ModelResponse, error) {
	var last error
	for attempt := 0; attempt < r.opts.MaxAttempts; attempt++ {
		if attempt > 0 {
			if err := r.wait(ctx, attempt, last); err != nil {
				return nil, err
			}
		}
		resp, err := r.inner.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}
		last = err
		if !r.opts.RetryIf(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("modelretry: 重试 %d 次后仍失败: %w", r.opts.MaxAttempts, last)
}

func (r *retryModel) wait(ctx context.Context, attempt int, cause error) error {
	delay := r.opts.BaseDelay << (attempt - 1)
	delay += time.Duration(float64(delay) * 0.2 * rand.Float64())
	if c, ok := ctx.(*ga.Context); ok {
		c.Emit(ga.Event{
			Type: EventRetry,
			Data: &RetryInfo{Attempt: attempt + 1, MaxAttempts: r.opts.MaxAttempts, Delay: delay, Err: cause},
		})
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type retryStreamModel struct {
	*retryModel
	stream ga.StreamModel
}

func (r *retryStreamModel) Stream(ctx context.Context, req *ga.ModelRequest) (ga.ModelStream, error) {
	var last error
	for attempt := 0; attempt < r.opts.MaxAttempts; attempt++ {
		if attempt > 0 {
			if err := r.wait(ctx, attempt, last); err != nil {
				return nil, err
			}
		}
		st, err := r.stream.Stream(ctx, req)
		if err == nil {
			return st, nil
		}
		last = err
		if !r.opts.RetryIf(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("modelretry: 重试 %d 次后仍失败: %w", r.opts.MaxAttempts, last)
}
