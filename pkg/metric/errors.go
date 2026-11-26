package metric

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidArgument reports an invalid caller-supplied argument.
	//
	// ErrInvalidArgument 表示调用方传入了无效参数。
	ErrInvalidArgument = errors.New("metric: invalid argument")
	// ErrNotFound reports a missing metric or record.
	//
	// ErrNotFound 表示指标或记录不存在。
	ErrNotFound = errors.New("metric: not found")
	// ErrNoData reports that a valid query range contained no samples.
	//
	// ErrNoData 表示有效查询范围内没有采样。
	ErrNoData = errors.New("metric: no data in range")
	// ErrAlreadyExists reports that a create-only operation found an existing row.
	//
	// ErrAlreadyExists 表示只创建操作遇到了已存在的行。
	ErrAlreadyExists = errors.New("metric: already exists")
	// ErrClosed reports that the store has already been closed.
	//
	// ErrClosed 表示 Store 已经关闭。
	ErrClosed = errors.New("metric: store is closed")
	// ErrWriteUnavailable means a nonblocking append could not be admitted
	// without waiting, or was not an append to the current raw window.
	ErrWriteUnavailable = errors.New("metric: nonblocking append unavailable")
	// ErrRawHistoryUnavailable means an exact upsert cannot be distinguished
	// from an observation already represented only by a persisted rollup.
	ErrRawHistoryUnavailable = errors.New("metric: exact history unavailable for upsert")
	// ErrImportPending means a bulk import overlaps observations not yet
	// persisted by this process. The caller must coordinate and retry.
	ErrImportPending = errors.New("metric: import overlaps pending observations")
	// ErrImportConflict means a source rollup differs from a durable row that
	// a conservative migration must preserve.
	ErrImportConflict = errors.New("metric: import conflicts with existing rollup")
	// ErrRebuildConflict means a rebuilt bucket differs from a durable parent;
	// retained fine-tier rows may no longer represent all of its observations.
	ErrRebuildConflict = errors.New("metric: rebuild conflicts with existing rollup")
	// ErrRetentionPending prevents retention from erasing the only in-memory
	// source needed by a coarser tier that has not yet expired.
	ErrRetentionPending = errors.New("metric: retention overlaps pending rollups")
	// ErrCommitUncertain means a transaction may have committed despite
	// returning an error. Retrying or flushing memory can corrupt history.
	ErrCommitUncertain = errors.New("metric: transaction commit is uncertain")
)

// WriteAcceptedError reports a maintenance failure after a batch was already
// admitted to memory. The caller must not replay the batch: its samples remain
// owned by the Store even if a flush failed or its commit outcome is unknown.
// Flush/Close can report or retry the retained state as appropriate.
type WriteAcceptedError struct{ Cause error }

func (e *WriteAcceptedError) Error() string {
	return fmt.Sprintf("metric: batch accepted but rollup maintenance failed: %v", e.Cause)
}

func (e *WriteAcceptedError) Unwrap() error { return e.Cause }
