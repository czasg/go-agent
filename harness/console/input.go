package console

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
)

// ErrClosed 表示输入流已关闭。
var ErrClosed = errors.New("harness: input closed")

// lineReader 是进程内唯一的一行输入源。
//
// 为什么必须唯一：ask / approve / REPL 若各自 bufio.NewScanner(os.Stdin)，
// 前一个 reader 会把后面几行预读进自己的缓冲区，导致另一个 reader 再也读不到。
// harness 统一持有一个 lineReader，所有要读键盘的地方都从这里取，
// 从根本上消除抢占。
//
// 读取放在常驻 goroutine 里，Read 只等结果，因此 ctx 取消能立刻返回，
// 不会像裸 scanner.Scan() 那样卡死在 stdin 上（Ctrl+C 打断本轮依赖这一点）。
type lineReader struct {
	sc   *bufio.Scanner
	ch   chan lineResult
	once sync.Once
}

type lineResult struct {
	text string
	err  error
}

func newLineReader(r io.Reader) *lineReader {
	lr := &lineReader{
		sc: bufio.NewScanner(r),
		// 缓冲若干行：Read 因 ctx 取消提前返回时，已读到的行留在缓冲里等下次
		// 取，不会丢。缓冲满则 scanner goroutine 阻塞——这正是我们要的背压，
		// 没人消费时不该继续猛读 stdin。
		ch: make(chan lineResult, 64),
	}
	// 长输入（粘贴大段文本）默认 64KB 不够用，放大到 1MB。
	lr.sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	go func() {
		for {
			if !lr.sc.Scan() {
				err := lr.sc.Err()
				if err == nil {
					err = io.EOF
				}
				lr.ch <- lineResult{err: err}
				return
			}
			// 通道有缓冲且消费方随用随取；这里发完立刻回去读下一行。
			lr.ch <- lineResult{text: lr.sc.Text()}
		}
	}()
	return lr
}

// Read 读取一行（已去掉首尾空白）。ctx 取消时返回 ctx.Err()，
// 但底层读取 goroutine 不中断（进程级共享，不能因为一次取消就废掉输入流）。
func (lr *lineReader) Read(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-lr.ch:
		if r.err != nil {
			if errors.Is(r.err, io.EOF) {
				return "", ErrClosed
			}
			return "", r.err
		}
		return strings.TrimSpace(r.text), nil
	}
}
