//go:build linux

// Package backupwatch checks a recipient-only receiver's bounded local heartbeat.
// It does not decrypt backups or treat a checksum as proof of recoverability.
package backupwatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	MaxBackupAge = 26 * time.Hour
	MaxReportAge = 45 * time.Minute
	ClockSkew    = 5 * time.Minute
	retryDelay   = 15 * time.Minute
	maxJSONBytes = 4096
)

type Report struct {
	Version    int        `json:"version"`
	CheckedAt  time.Time  `json:"checked_at"`
	ReceivedAt time.Time  `json:"received_at"`
	Status     string     `json:"status"`
	BackupAt   *time.Time `json:"backup_at"`
}

type state struct {
	Version       int       `json:"version"`
	Observed      string    `json:"observed"`
	Notified      string    `json:"notified"`
	AttemptReason string    `json:"attempt_reason"`
	LastAttempt   time.Time `json:"last_attempt"`
}

type Result struct {
	Reason      string
	Changed     bool
	Sent        bool
	SendFailed  bool
	StateFailed bool
}

// Watch serializes overlapping scheduler runs. The state is persisted beside the
// heartbeat, so an ordinary Panel restart does not resend an ongoing incident.
type Watch struct {
	mu sync.Mutex
}

func readJSON(path string, destination any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > maxJSONBytes {
		return errors.New("unsafe or oversized health file")
	}
	descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("health file changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxJSONBytes+1))
	if err != nil || len(raw) > maxJSONBytes {
		return errors.New("cannot read bounded health file")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing health JSON")
	}
	return nil
}

func safeDirectory(directory string) error {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() {
			return errors.New("health path must use real directories")
		}
		if current == absolute && info.Mode().Perm()&0077 != 0 {
			return errors.New("health directory must be private")
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

func assess(path string, now time.Time) string {
	var report Report
	if err := readJSON(path, &report); err != nil {
		if os.IsNotExist(err) {
			return "report_missing"
		}
		return "report_invalid"
	}
	if report.Version != 1 || report.CheckedAt.IsZero() || report.ReceivedAt.IsZero() ||
		report.CheckedAt.After(now.Add(ClockSkew)) || report.ReceivedAt.After(now.Add(ClockSkew)) ||
		report.CheckedAt.Sub(report.ReceivedAt).Abs() > ClockSkew {
		return "report_invalid"
	}
	if now.Sub(report.CheckedAt) > MaxReportAge || now.Sub(report.ReceivedAt) > MaxReportAge {
		return "report_stale"
	}
	switch report.Status {
	case "missing", "invalid":
		if report.BackupAt != nil {
			return "report_invalid"
		}
		return "backup_" + report.Status
	case "ok":
		if report.BackupAt == nil || report.BackupAt.IsZero() ||
			report.BackupAt.After(now.Add(ClockSkew)) || report.BackupAt.After(report.CheckedAt.Add(ClockSkew)) {
			return "report_invalid"
		}
		if now.Sub(*report.BackupAt) > MaxBackupAge {
			return "backup_stale"
		}
		return "ok"
	default:
		return "report_invalid"
	}
}

func saveState(path string, value state) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".alert-state-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func notice(reason string) (string, string) {
	if reason == "ok" {
		return "Nekomari backup recovered", "OVH encrypted backup checks and reporting are fresh again. This checks ciphertext and its local checksum, not decryption or restore."
	}
	return "Nekomari backup needs attention", fmt.Sprintf("OVH encrypted backup health: %s. Limits: backup 26 hours, report 45 minutes. Inspect the backup receiver and health timers on OVH. Do not delete recovery copies or export the identity.", reason)
}

// Check only sends on an incident change or recovery; unsuccessful sends retry
// after 15 minutes. Persisting the pending attempt before sending bounds retries
// after a crash. External delivery and local state cannot be exactly-once atomic.
func (w *Watch) Check(path string, now time.Time, send func(message, title string) error) Result {
	if !w.mu.TryLock() {
		return Result{}
	}
	defer w.mu.Unlock()
	result := Result{}
	if err := safeDirectory(filepath.Dir(path)); err != nil {
		result.Reason, result.StateFailed = "report_invalid", true
		return result
	}
	result.Reason = assess(path, now)
	statePath := filepath.Join(filepath.Dir(path), "alert-state.json")
	var previous state
	if err := readJSON(statePath, &previous); err != nil && !os.IsNotExist(err) {
		result.StateFailed = true
		return result
	}
	if previous.Version != 0 && previous.Version != 1 {
		result.StateFailed = true
		return result
	}
	next := previous
	next.Version = 1
	next.Observed = result.Reason
	result.Changed = previous.Observed != result.Reason
	wantSend := (result.Reason != "ok" && previous.Notified != result.Reason) ||
		(result.Reason == "ok" && previous.Notified != "" && previous.Notified != "ok")
	if wantSend && previous.AttemptReason == result.Reason &&
		!previous.LastAttempt.IsZero() && !previous.LastAttempt.After(now.Add(ClockSkew)) &&
		now.Sub(previous.LastAttempt) < retryDelay {
		wantSend = false
	}
	if wantSend {
		next.AttemptReason, next.LastAttempt = result.Reason, now
	}
	if next != previous {
		if err := saveState(statePath, next); err != nil {
			result.StateFailed = true
			return result
		}
	}
	if !wantSend {
		return result
	}
	title, message := notice(result.Reason)
	if err := send(message, title); err != nil {
		result.SendFailed = true
		return result
	}
	next.Notified = result.Reason
	result.Sent = true
	if err := saveState(statePath, next); err != nil {
		result.StateFailed = true
	}
	return result
}
