package jsonrpc

import (
	"context"
	"strconv"
	"strings"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/internal/bulk"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/database/auditlog"
)

// admin.bulk.go
// One change applied to many nodes, with a per-node outcome.
//
// The applier is `clients.SaveClientInfo` — the same function `admin:editClient` calls — so
// there is one implementation of what a valid client update is. This handler adds the
// fan-out, the per-node reporting and the audit entry, and nothing else.

// maxBulkClients bounds one request. The fleet is ten nodes; a caller asking to edit
// thousands is either a bug or an attempt to make the panel do a lot of work, and either
// way a bound is cheaper than discovering it.
const maxBulkClients = 256

func init() {
	RegisterWithGroupAndMeta("bulkEditClients", rpc.RoleAdmin, adminBulkEditClients, &rpc.MethodMeta{
		Name:    "admin:bulkEditClients",
		Summary: "Apply one partial update to many clients, reporting each outcome",
		Params: []rpc.ParamMeta{
			{Name: "uuids", Type: "string[]", Required: true, Description: "Client UUIDs to update"},
			{Name: "update", Type: "object", Required: true, Description: "Partial client fields, without uuid"},
		},
		Returns: "Report; applied/failed counts plus one outcome per uuid, each carrying the applier's own error",
	})
}

type adminBulkEditParams struct {
	UUIDs  []string               `json:"uuids"`
	Update map[string]interface{} `json:"update"`
}

func adminBulkEditClients(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params adminBulkEditParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if len(params.UUIDs) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuids is required", nil)
	}
	if len(params.UUIDs) > maxBulkClients {
		return nil, rpc.MakeError(rpc.InvalidParams, "too many clients in one bulk edit", nil)
	}

	report, err := bulk.Apply(clients.ClientInfoApplier{}, params.UUIDs, params.Update)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}

	// Logged with the counts and the field names, not the values: the audit trail records
	// that a change happened and to how many nodes, and putting a selection's values into it
	// would write fleet configuration into the logs.
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor,
		"bulk edit clients: applied="+strconv.Itoa(report.Applied)+
			" failed="+strconv.Itoa(report.Failed)+
			" fields="+strings.Join(report.FieldNames, ","),
		"warn")
	return report, nil
}
