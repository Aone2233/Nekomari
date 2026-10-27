package terminal

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/web/connection"
)

func TestCloseClientSessionsRetiresOnlyMatchingGeneration(t *testing.T) {
	old := &TerminalSession{UUID: "retired-client"}
	other := &TerminalSession{UUID: "other-client"}
	TerminalSessionsMutex.Lock()
	TerminalSessions["retired-session"], TerminalSessions["other-session"] = old, other
	scheduleCleanup("retired-session", old)
	TerminalSessionsMutex.Unlock()
	t.Cleanup(func() { closeSession("retired-session"); closeSession("other-session") })
	CloseClientSessions(old.UUID)
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	if TerminalSessions["retired-session"] != nil || old.CleanupTimer != nil || TerminalSessions["other-session"] != other {
		t.Fatal("credential reset retained retired session/timer or affected another node")
	}
}

func TestAttachAgentRefusesRetiredSessionIdentity(t *testing.T) {
	old := &TerminalSession{UUID: "same-client"}
	replacement := &TerminalSession{UUID: "same-client", CleanupTimer: time.NewTimer(time.Hour)}
	defer replacement.CleanupTimer.Stop()
	TerminalSessionsMutex.Lock()
	TerminalSessions["reused-session"] = replacement
	TerminalSessionsMutex.Unlock()
	t.Cleanup(func() { closeSession("reused-session") })
	if _, ok := attachAgent("reused-session", old, &connection.SafeConn{}); ok {
		t.Fatal("old handshake attached to replacement session")
	}
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	if replacement.Agent != nil {
		t.Fatal("retired handshake changed replacement session")
	}
}
