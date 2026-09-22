package jsonrpc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/web/upload"
)

func TestUploadStatsRequiresAdmin(t *testing.T) {
	for _, role := range []string{rpc.RoleGuest, rpc.RoleClient} {
		response := OnInternalRequest(context.Background(), role, "admin:getUploadStats", nil)
		if response.Error == nil || response.Error.Code != rpc.PermissionDenied {
			t.Fatalf("role %s accessed upload stats: %+v", role, response)
		}
	}
}

func TestUploadStatsReturnsCachedSnapshot(t *testing.T) {
	previous := upload.DefaultStore
	upload.DefaultStore = &upload.Store{Root: filepath.Join(t.TempDir(), "absent"), MaxSize: 1024}
	t.Cleanup(func() { upload.DefaultStore = previous })
	if err := upload.DefaultStore.CleanupExpired(); err != nil {
		t.Fatal(err)
	}
	want := upload.DefaultStore.Stats()
	if want.LastScan.IsZero() {
		t.Fatal("missing scan freshness timestamp")
	}
	response := OnInternalRequest(context.Background(), rpc.RoleAdmin, "admin:getUploadStats", nil)
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	gotJSON, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("got %s; want %s", gotJSON, wantJSON)
	}
}
