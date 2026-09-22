package jsonrpc

import (
	"context"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/web/upload"
)

func init() {
	RegisterWithGroupAndMeta("getUploadStats", rpc.RoleAdmin, adminGetUploadStats, &rpc.MethodMeta{
		Name:    "admin:getUploadStats",
		Summary: "Read cached upload cleanup and contention statistics without disk I/O",
		Returns: "CleanupStats; durations are nanoseconds, timestamps are RFC3339; reservations describe last_scan, not allocated disk bytes",
	})
}

func adminGetUploadStats(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return upload.DefaultStore.Stats(), nil
}
