package server

import (
	"math/rand"
	"sync/atomic"
	"time"
)

// 重连退避。
//
// 改动前每一次重连和每一次重试都按固定的 --reconnect-interval（默认 5 秒）来，永远
// 如此：面板挂掉时，每个节点每 5 秒敲一次 WebSocket、每 5 秒发一次注定失败的 POST
// 报告、每 5 秒再拉一次任务 —— 而且每次都写一行日志。十台节点的规模下这只是吵，但
// 形状是错的：真挂久了，日志与连接尝试会按节点数线性堆上去，而这段时间里每一次尝试
// 都注定失败。
//
// 规则：5 秒起步，每次失败翻倍，封顶 2 分钟，±25% 抖动，成功即重置。
//
// 抖动不是装饰：所有节点同时开始重试、又用同一个间隔，就会一直同相，面板一恢复那
// 一秒会被同一批连接同时打一遍 —— 而刚恢复的时候正是它最脆弱的时候。
const (
	reconnectBaseDelay = 5 * time.Second
	reconnectMaxDelay  = 2 * time.Minute
)

// failureCounter 统计连续失败次数，用于退避与日志限流。
//
// 分开计数而不是共用一个全局值：WebSocket 连不上、报告发不出去、任务拉不回来是
// 三条独立的路径，其中一条的失败不应该把另一条的重试节奏也拖慢。
type failureCounter struct{ n int64 }

// fail 记一次失败并返回当前连续失败次数。
func (c *failureCounter) fail() int { return int(atomic.AddInt64(&c.n, 1)) }

// reset 清零：成功之后下一次失败从 5 秒重新起步。
func (c *failureCounter) reset() { atomic.StoreInt64(&c.n, 0) }

// current 返回当前连续失败次数，用于决定第一次重试的等待时长。
func (c *failureCounter) current() int { return int(atomic.LoadInt64(&c.n)) }

var (
	wsConnectFailures failureCounter // WebSocket 连接
	v2PullFailures    failureCounter // 兜底模式下的任务拉取
)

// reconnectBackoff 给出第 failures 次失败之后应等待的时长。
//
// 返回值始终落在 [reconnectBaseDelay, reconnectMaxDelay] 内：抖动只在这个区间里
// 挪动，不把上限再放出去 —— 否则「封顶两分钟」就成了一句不准的说明。
func reconnectBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	// 左移到超过上限就够了，再移会溢出成负数。
	shift := failures - 1
	if shift > 8 {
		shift = 8
	}
	delay := reconnectBaseDelay << shift
	if delay <= 0 || delay > reconnectMaxDelay {
		delay = reconnectMaxDelay
	}
	quarter := int64(delay) / 4
	if quarter > 0 {
		delay += time.Duration(rand.Int63n(2*quarter)) - time.Duration(quarter)
	}
	if delay > reconnectMaxDelay {
		delay = reconnectMaxDelay
	}
	if delay < reconnectBaseDelay {
		delay = reconnectBaseDelay
	}
	return delay
}
