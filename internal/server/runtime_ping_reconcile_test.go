package server

import (
	"errors"
	"testing"
)

func TestPingReconciliationRegistrationIsRequired(t *testing.T) {
	cause := errors.New("scheduler unavailable")
	if err := registerPingReconciliation(func(name, spec string, callback func()) error {
		if name != "ping-reconcile" || spec != "@every 30s" || callback == nil {
			t.Fatalf("unexpected reconciliation registration: %s %s", name, spec)
		}
		return cause
	}); !errors.Is(err, cause) {
		t.Fatalf("startup would hide reconciliation registration failure: %v", err)
	}
}
