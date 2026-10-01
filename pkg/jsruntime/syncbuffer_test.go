package jsruntime

import (
	"bytes"
	"sync"
)

// syncBuffer 是并发安全的输出接收器，用于把 Console 的输出接进测试。
//
// 为什么不能直接用 bytes.Buffer：JS 的定时器回调跑在事件循环的后台协程里，出错时
// 会经 console.Report 往 Console 写；测试协程随后又要读同一个 buffer。裸
// bytes.Buffer 在这种用法下就是数据竞争——现场实测
// TestHTTPClientRequestErrorWithoutListenerStillCloses 在 -race 下被判定失败
// （写侧在 console.write 的 Fprintln，读侧在测试的 output.String()）。
//
// console 模块自己已经串行化了它这一侧的写入（见 console.Module.writeMu），
// 但那只保证"两个写者不会互相破坏"，管不到调用方的读——读侧的同步只能由调用方
// 自己保证，这就是这个类型存在的理由。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
