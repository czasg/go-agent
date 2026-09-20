package util

import (
	"encoding/json"
	"fmt"
	"sync"
)

func PtrOf[T any](v T) *T {
	return &v
}

func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// Coalesce 优先级取值：opt > def > 零值。
func Coalesce[T any](def *T, opt *T) T {
	if opt != nil {
		return *opt
	}
	if def != nil {
		return *def
	}
	var zero T
	return zero
}

func PPrint(v interface{}) {
	if s, ok := v.(string); ok {
		fmt.Println(s)
		return
	}
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

func NewOncePrint(v any) func(data any) {
	var once sync.Once
	return func(data any) {
		once.Do(func() {
			PPrint(v)
		})
		fmt.Print(data)
	}
}
