package jsonrpc

import (
	"context"
	"testing"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

// A deleted node keeps its retained history in the metric store, so "absent from
// the client table" must not be read as "exists and is not hidden": an anonymous
// caller could otherwise request an unknown or deleted UUID and read history the
// panel no longer lists. Hidden nodes stay readable to a logged-in user.
func TestPublicHistoryRejectsUnknownClientUUID(t *testing.T) {
	db := dbcore.GetDBInstance()
	for _, client := range []models.Client{
		{UUID: "history-known", Name: "known", Token: "test-only-token-known"},
		{UUID: "history-hidden", Name: "hidden", Token: "test-only-token-hidden", Hidden: true},
	} {
		if err := db.Create(&client).Error; err != nil {
			t.Fatal(err)
		}
	}
	// The revision watcher is not registered in this test binary, so make the
	// visibility cache re-read the rows inserted above.
	dbcache.InvalidateAll()

	guest := rpc.NewContextWithMeta(context.Background(), &rpc.ContextMeta{Principal: rpc.NewAnonymousPrincipal()})
	loggedIn := rpc.NewContextWithMeta(context.Background(), &rpc.ContextMeta{Principal: rpc.PrincipalFromRole(rpc.RoleAdmin)})
	requested := []string{"history-known", "history-hidden", "history-deleted"}

	ids, jerr := publicMetricEntityIDs(guest, requested)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if len(ids) != 1 || ids[0] != "history-known" {
		t.Fatalf("guest entity admission = %v, want only the existing, non-hidden node", ids)
	}

	ids, jerr = publicMetricEntityIDs(loggedIn, requested)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if len(ids) != 2 {
		t.Fatalf("logged-in entity admission = %v, want the two existing nodes", ids)
	}
	for _, id := range ids {
		if id == "history-deleted" {
			t.Fatal("a deleted UUID passed entity admission")
		}
	}

	records := &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "public:getRecordsByUUID",
		Params: map[string]any{"uuid": "history-deleted", "hours": "1"},
	}
	if _, jerr := publicGetRecordsByUUID(guest, records); jerr == nil || jerr.Code != rpc.InvalidParams {
		t.Fatalf("deleted UUID served to the load-history endpoint: %v", jerr)
	}

	recent := &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "public:getClientRecentRecords",
		Params: map[string]any{"uuid": "history-deleted"},
	}
	if _, jerr := publicGetClientRecentRecords(guest, recent); jerr == nil || jerr.Code != rpc.InvalidParams {
		t.Fatalf("deleted UUID served to the recent-records endpoint: %v", jerr)
	}

	// A hidden node answers exactly like an unknown one, so the response cannot be
	// used to probe which UUIDs exist.
	hidden := &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "public:getRecordsByUUID",
		Params: map[string]any{"uuid": "history-hidden", "hours": "1"},
	}
	if _, jerr := publicGetRecordsByUUID(guest, hidden); jerr == nil || jerr.Code != rpc.InvalidParams {
		t.Fatalf("hidden UUID answered differently from an unknown one: %v", jerr)
	}
}
