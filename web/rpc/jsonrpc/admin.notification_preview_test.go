package jsonrpc

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils/notifier"
)

func TestLoadPreviewPermissionsStateAndBusy(t *testing.T) {
	db := dbcore.GetDBInstance()
	last := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	rule := models.LoadNotification{Name: "preview-test", Clients: models.StringArray{"preview-node"}, Metric: "cpu", Threshold: 80, Ratio: .8, Interval: 15, LastNotified: &last}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Delete(&rule) })
	invoke := func(principal *rpc.Principal, params any) *rpc.JsonRpcResponse {
		return Dispatch(context.Background(), &rpc.ContextMeta{Principal: principal}, rpc.NewRequest(1, "admin:previewLoadNotification", params))
	}
	for _, p := range []*rpc.Principal{rpc.NewAnonymousPrincipal(), rpc.NewAgentPrincipal("node"), rpc.NewScopedAPIKeyPrincipal("key", rpc.APIKeyScopes())} {
		result := invoke(p, map[string]any{"id": rule.Id})
		if result.Error == nil || result.Error.Code != rpc.PermissionDenied {
			t.Fatalf("principal %v allowed: %+v", p.Type, result)
		}
	}
	admin := rpc.NewUserPrincipal("admin")
	if resp := invoke(admin, map[string]any{}); resp.Error == nil || resp.Error.Code != rpc.InvalidParams {
		t.Fatalf("invalid: %+v", resp)
	}
	if resp := invoke(admin, map[string]any{"id": uint(999999999)}); resp.Error == nil || resp.Error.Code != rpc.NotFound {
		t.Fatalf("missing: %+v", resp)
	}
	resp := invoke(admin, map[string]any{"id": rule.Id})
	if resp.Error != nil {
		t.Fatalf("preview: %+v", resp.Error)
	}
	result, ok := resp.Result.(notifier.LoadPreview)
	if !ok || len(result.Targets) != 1 || result.Targets[0].Status != "query_error" || !result.StoredCooldownActive {
		t.Fatalf("result: %+v", resp.Result)
	}
	var after models.LoadNotification
	if err := db.First(&after, rule.Id).Error; err != nil || after.LastNotified == nil || !after.LastNotified.Equal(last) {
		t.Fatalf("last notified changed: %+v %v", after.LastNotified, err)
	}
	loadPreviewSlot <- struct{}{}
	busy := invoke(admin, map[string]any{"id": rule.Id})
	<-loadPreviewSlot
	if busy.Error == nil || busy.Error.Code != rpc.Unavailable {
		t.Fatalf("busy: %+v", busy)
	}
}
