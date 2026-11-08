package agent

import (
	"crypto/sha256"
	"sync"
	"time"

	logger "github.com/Aone2233/nekomari/utils/log"
)

type pinnedObservationKey struct {
	stage string
	task  uint
	node  [32]byte
}

type pinnedObservationLimiter struct {
	mu     sync.Mutex
	minute int64
	used   int
	last   map[pinnedObservationKey]time.Time
}

// Bound global volume and per-node repetition, including when old Agents are
// assigned. Never log targets, credentials, payloads or arbitrary errors.
func (l *pinnedObservationLimiter) allow(key pinnedObservationKey, now time.Time) bool {
	if !l.mu.TryLock() {
		return false
	}
	defer l.mu.Unlock()
	minute := now.Unix() / 60
	if l.minute != minute {
		l.minute, l.used = minute, 0
	}
	if l.used >= 16 {
		return false
	}
	if last, ok := l.last[key]; ok && now.Sub(last) < 10*time.Minute {
		return false
	}
	if l.last == nil {
		l.last = make(map[pinnedObservationKey]time.Time)
	}
	if len(l.last) >= 256 {
		for k, last := range l.last {
			if now.Sub(last) >= 10*time.Minute {
				delete(l.last, k)
			}
		}
		if len(l.last) >= 256 {
			return false
		}
	}
	l.last[key], l.used = now, l.used+1
	return true
}

var pinnedObservations pinnedObservationLimiter

func observePinnedFailure(stage string, taskID uint, uuid string) {
	if taskID == 0 || uuid == "" {
		return
	}
	switch stage {
	case "history", "permit_busy", "permit_reserve", "permit_write", "permit_response", "permit_timeout", "permit_context", "delivery", "result_write", "result_admission":
	default:
		return
	}
	key := pinnedObservationKey{stage: stage, task: taskID, node: sha256.Sum256([]byte(uuid))}
	if pinnedObservations.allow(key, time.Now()) {
		logger.Warnf("pinned-probe", "probe skipped stage=%s task=%d node=%x", stage, taskID, key.node[:4])
	}
}

// ObservePinnedAdmissionFailure is called only after credential admission is
// released. The error itself is deliberately not logged.
func ObservePinnedAdmissionFailure(taskID uint, uuid string, writeUnavailable bool) {
	stage := "result_admission"
	if writeUnavailable {
		stage = "result_write"
	}
	observePinnedFailure(stage, taskID, uuid)
}
