package jsonrpc

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/Aone2233/nekomari/utils/messageSender/factory"
)

// recordSender is a message sender that records structured events instead of
// delivering them, so the rule test-send can be asserted without a real channel.
type recordSender struct {
	mu     sync.Mutex
	events []models.EventMessage
}

func (s *recordSender) GetName() string                         { return "record-test" }
func (s *recordSender) GetConfiguration() factory.Configuration { return &struct{}{} }
func (s *recordSender) Init() error                             { return nil }
func (s *recordSender) Destroy() error                          { return nil }
func (s *recordSender) SendTextMessage(string, string) error    { return nil }

func (s *recordSender) SendEvent(event models.EventMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordSender) sent() []models.EventMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]models.EventMessage(nil), s.events...)
}

func TestAdminTestLoadNotification(t *testing.T) {
	recorder := &recordSender{}
	factory.RegisterMessageSender(func() factory.IMessageSender { return recorder })
	if err := messageSender.LoadProvider("record-test", "{}"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := messageSender.LoadProvider("empty", "{}"); err != nil {
			t.Fatal(err)
		}
	})
	previous, err := config.GetAs[bool](config.NotificationEnabledKey, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Set(config.NotificationEnabledKey, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set(config.NotificationEnabledKey, previous) })

	db := dbcore.GetDBInstance()
	rule := models.LoadNotification{Name: "test-send-rule", Metric: "cpu", Threshold: 80, Ratio: .8, Interval: 15}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Delete(&rule) })

	invoke := func(principal *rpc.Principal, params any) *rpc.JsonRpcResponse {
		return Dispatch(context.Background(), &rpc.ContextMeta{Principal: principal}, rpc.NewRequest(1, "admin:testLoadNotification", params))
	}
	for _, principal := range []*rpc.Principal{rpc.NewAnonymousPrincipal(), rpc.NewAgentPrincipal("node"), rpc.NewScopedAPIKeyPrincipal("key", rpc.APIKeyScopes())} {
		if resp := invoke(principal, map[string]any{"id": rule.Id}); resp.Error == nil || resp.Error.Code != rpc.PermissionDenied {
			t.Fatalf("principal %v allowed: %+v", principal.Type, resp)
		}
	}
	admin := rpc.NewUserPrincipal("admin")
	if resp := invoke(admin, map[string]any{}); resp.Error == nil || resp.Error.Code != rpc.InvalidParams {
		t.Fatalf("invalid: %+v", resp)
	}
	if resp := invoke(admin, map[string]any{"id": uint(999999999)}); resp.Error == nil || resp.Error.Code != rpc.NotFound {
		t.Fatalf("missing rule: %+v", resp)
	}

	resp := invoke(admin, map[string]any{"id": rule.Id})
	if resp.Error != nil {
		t.Fatalf("test send: %+v", resp.Error)
	}
	events := recorder.sent()
	if len(events) != 1 {
		t.Fatalf("sent %d events, want 1", len(events))
	}
	if msg, ok := events[0].Message.(string); !ok || !strings.HasPrefix(msg, "[TEST] ") || !strings.HasSuffix(msg, rule.Name) {
		t.Fatalf("message = %#v, want a labelled test for %q", events[0].Message, rule.Name)
	}
	if drift := time.Since(events[0].Time); drift < -time.Second || drift > 30*time.Second {
		t.Fatalf("time = %v, want now (drift %v)", events[0].Time, drift)
	}

	// The rule's own cooldown state must not move: a channel test is not an alert.
	var after models.LoadNotification
	if err := db.First(&after, rule.Id).Error; err != nil {
		t.Fatal(err)
	}
	if after.LastNotified != nil {
		t.Fatalf("last_notified changed to %v", after.LastNotified)
	}

	// One send per cooldown, fleet-wide, independent of the rule being tested.
	limited := invoke(admin, map[string]any{"id": rule.Id})
	if limited.Error == nil || limited.Error.Code != rpc.Unavailable || !strings.Contains(limited.Error.Message, "rate-limited") {
		t.Fatalf("second send = %+v, want rate-limit refusal", limited.Error)
	}
	if len(recorder.sent()) != 1 {
		t.Fatalf("rate-limited send delivered anyway: %+v", recorder.sent())
	}
}
