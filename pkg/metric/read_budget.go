package metric

import (
	"context"
	"errors"
	"sync/atomic"
)

type readBudgetKey struct{}

var ErrReadBudget = errors.New("metric read budget exceeded; narrow the query")

// estimatedBytesPerRow is the measured transient allocation of one materialized
// metric row, used to give the read budget a byte dimension. It comes from the
// 2026-10-18 memory audit (docs/audits/2026-10-18/backend-memory.md §3.1): a
// 3 h x 14 metric x 9 node rollup read allocated 12.1 MB for 22,680 rows, i.e.
// ~534 B per row. The number covers the accumulator state a read materializes
// (rollup bucket, t-digest, decoded tag map) rather than the wire payload.
//
// estimatedBytesPerRow 是一行物化指标实测的瞬时分配量，用于给读取预算增加字节
// 维度。数值来自 2026-10-18 内存审计：3 小时 × 14 指标 × 9 节点的 rollup 读取为
// 22,680 行分配 12.1 MB，约 534 B/行。该数字衡量读取物化的累加器状态（rollup
// 桶、t-digest、解码后的标签 map），而不是传输载荷。
const estimatedBytesPerRow = 534

// readBudgetState is the mutable spend state of one bounded request. A budget
// always carries a row allowance and, when the caller supplies one, a byte
// allowance; exhausting either dimension ends the read with ErrReadBudget.
//
// readBudgetState 是一次受限请求的可变消耗状态。预算总是包含行额度，调用方提供
// 时还包含字节额度；任一维度耗尽都会以 ErrReadBudget 结束读取。
type readBudgetState struct {
	remainingRows  atomic.Int64
	remainingBytes atomic.Int64
	bytesEnabled   bool
}

// WithReadBudget bounds materialized samples and rollup rows across all reads
// in one public request, including tag-expanded series. Internal callers can
// omit it for maintenance operations.
//
// It is the row-only form of WithReadBudgetBytes, kept because most callers and
// tests only need the row ceiling.
//
// WithReadBudget 在一次公共请求的所有读取之间限制物化的采样点与 rollup 行数，
// 包括按标签展开的序列。内部调用方可在维护操作中省略它。
//
// 它是 WithReadBudgetBytes 的“仅行数”形式，保留是因为多数调用方与测试只需要
// 行数上限。
func WithReadBudget(ctx context.Context, limit int64) context.Context {
	return WithReadBudgetBytes(ctx, limit, 0)
}

// WithReadBudgetBytes bounds a request by rows and by estimated bytes. maxBytes
// <= 0 disables the byte dimension, so a caller that only cares about rows gets
// the historical behaviour. Each spend of n rows charges n rows and
// n*estimatedBytesPerRow bytes; the first dimension to run out returns
// ErrReadBudget.
//
// WithReadBudgetBytes 同时按行数与估算字节数限制一次请求。maxBytes <= 0 表示
// 关闭字节维度，因此只关心行数的调用方得到与历史一致的行为。每次消耗 n 行会
// 同时扣减 n 行与 n*estimatedBytesPerRow 字节；先耗尽的维度返回 ErrReadBudget。
func WithReadBudgetBytes(ctx context.Context, maxItems, maxBytes int64) context.Context {
	state := &readBudgetState{bytesEnabled: maxBytes > 0}
	state.remainingRows.Store(maxItems)
	if state.bytesEnabled {
		state.remainingBytes.Store(maxBytes)
	}
	return context.WithValue(ctx, readBudgetKey{}, state)
}

// spendReadBudget charges n materialized rows against the request budget. A
// cancelled request reports its own error first: the caller's cancellation is
// the real reason the read stopped, and "narrow the query" would be wrong.
//
// spendReadBudget 按 n 行物化数据扣减请求预算。已被取消的请求先返回其自身错误：
// 调用方的取消才是读取停止的真正原因，“缩小查询”在这种情况下的建议是错的。
func spendReadBudget(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, ok := ctx.Value(readBudgetKey{}).(*readBudgetState)
	if !ok {
		return nil
	}
	if state.remainingRows.Add(-int64(n)) < 0 {
		return ErrReadBudget
	}
	if state.bytesEnabled && state.remainingBytes.Add(-int64(n)*estimatedBytesPerRow) < 0 {
		return ErrReadBudget
	}
	return nil
}
