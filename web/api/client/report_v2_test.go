package client

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

// reportBody is one agent.report request roughly the size the fleet sends:
// every metric section, a message and an ack list.
const reportBody = `{
	"jsonrpc": "2.0",
	"method": "agent.report",
	"params": {
		"report": {
			"uuid": "5f2b6c1e-3d4a-4c8b-9f0e-2a1b3c4d5e6f",
			"cpu": {"name": "AMD EPYC 7443P 24-Core Processor", "cores": 48, "arch": "amd64", "usage": 17.35},
			"ram": {"total": 67432136704, "used": 21474836480},
			"swap": {"total": 8589934592, "used": 0},
			"load": {"load1": 0.42, "load5": 0.51, "load15": 0.63},
			"disk": {"total": 536870912000, "used": 128849018880},
			"network": {"up": 1048576, "down": 4194304, "totalUp": 9007199254740993, "totalDown": 36028797018963968},
			"connections": {"tcp": 128, "udp": 12},
			"gpu": {"count": 1, "average_usage": 3.5, "detailed_info": [{"name": "NVIDIA GeForce RTX 4090", "memory_total": 25769803776, "memory_used": 1073741824, "utilization": 3.5, "temperature": 41}]},
			"uptime": 1234567,
			"process": 231,
			"message": "",
			"updated_at": "2026-09-22T10:00:00Z"
		},
		"ack_event_ids": ["evt-1", "evt-2", "evt-3"]
	},
	"id": 4211
}`

// TestHandleV2RPCRejectsMalformedParamsBeforeIngest pins the error the ingest
// path returns when the typed params cannot be decoded. No database is touched:
// the decode fails first.
func TestHandleV2RPCRejectsMalformedParamsBeforeIngest(t *testing.T) {
	resp := handleV2RPC("client-x", v2.RawRequest{
		JSONRPC: v2.Version,
		Method:  v2.MethodAgentReport,
		Params:  json.RawMessage(`{"report":`),
		ID:      1,
	}, false)
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("malformed params response = %+v", resp)
	}

	wrongVersion := handleV2RPC("client-x", v2.RawRequest{JSONRPC: "1.0", Method: v2.MethodAgentReport}, false)
	if wrongVersion.Error == nil || wrongVersion.Error.Code != -32600 {
		t.Fatalf("wrong version response = %+v", wrongVersion)
	}

	unknown := handleV2RPC("client-x", v2.RawRequest{JSONRPC: v2.Version, Method: "agent.nope"}, false)
	if unknown.Error == nil || unknown.Error.Code != -32601 {
		t.Fatalf("unknown method response = %+v", unknown)
	}
}

// TestReportBodyDecodesToTypedParams verifies the ingest path decodes the typed
// params straight from the wire bytes, without the untyped map in between.
func TestReportBodyDecodesToTypedParams(t *testing.T) {
	var req v2.RawRequest
	if err := json.Unmarshal([]byte(reportBody), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	var params v2.ReportParams
	if err := req.DecodeParams(&params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Report.UUID != "5f2b6c1e-3d4a-4c8b-9f0e-2a1b3c4d5e6f" {
		t.Fatalf("report uuid = %q", params.Report.UUID)
	}
	if params.Report.Network.TotalUp != 9007199254740993 {
		t.Fatalf("total_up = %d, want the exact counter", params.Report.Network.TotalUp)
	}
	if params.Report.GPU == nil || len(params.Report.GPU.DetailedInfo) != 1 {
		t.Fatalf("gpu = %+v", params.Report.GPU)
	}
	if len(params.AckEventIDs) != 3 {
		t.Fatalf("ack ids = %v", params.AckEventIDs)
	}
}

// BenchmarkReportParamsDecode compares the two ways of getting typed params out
// of one report body: the untyped-map round trip this path used to do, and the
// direct decode it does now.
//
// Run with:
//
//	go test ./web/api/client/ -run '^$' -bench ReportParamsDecode -benchmem
func BenchmarkReportParamsDecode(b *testing.B) {
	body := []byte(reportBody)

	b.Run("untyped_map_round_trip", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var req v2.Request
			if err := json.Unmarshal(body, &req); err != nil {
				b.Fatal(err)
			}
			encoded, err := json.Marshal(req.Params)
			if err != nil {
				b.Fatal(err)
			}
			var params v2.ReportParams
			if err := json.Unmarshal(encoded, &params); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("raw_params", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var req v2.RawRequest
			if err := json.Unmarshal(body, &req); err != nil {
				b.Fatal(err)
			}
			var params v2.ReportParams
			if err := req.DecodeParams(&params); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// recordingAgentUpdateWriter stands in for a v2 connection: it records what the
// panel would put on the wire and can be told to fail the write.
type recordingAgentUpdateWriter struct {
	written []any
	err     error
}

func (w *recordingAgentUpdateWriter) WriteJSON(v any) error {
	if w.err != nil {
		return w.err
	}
	w.written = append(w.written, v)
	return nil
}

// The connect-time re-send is what makes a rollout survive an agent restart: the
// agent keeps the target in memory only, so a node that reconnects must be told
// again. This pins the exact event the panel sends.
func TestResendPersistedAgentUpdateTargetSendsThePinnedVersion(t *testing.T) {
	writer := &recordingAgentUpdateWriter{}
	lookup := func(uuid string) (string, error) {
		if uuid != "node-1" {
			t.Fatalf("lookup uuid = %q", uuid)
		}
		return "v0.1.41", nil
	}
	if err := resendPersistedAgentUpdateTarget(writer, "node-1", lookup); err != nil {
		t.Fatalf("re-send: %v", err)
	}
	if len(writer.written) != 1 {
		t.Fatalf("wrote %d payloads, want 1", len(writer.written))
	}
	request, ok := writer.written[0].(v2.Request)
	if !ok {
		t.Fatalf("payload = %T, want v2.Request", writer.written[0])
	}
	if request.JSONRPC != v2.Version || request.Method != v2.MethodAgentUpdate {
		t.Fatalf("payload = %+v, want a %s request", request, v2.MethodAgentUpdate)
	}
	params, ok := request.Params.(v2.UpdateParams)
	if !ok {
		t.Fatalf("params = %T, want v2.UpdateParams", request.Params)
	}
	if params.Target != "v0.1.41" {
		t.Fatalf("target = %q, want v0.1.41", params.Target)
	}
}

// A cleared target must send nothing: the agent's default is already "follow the
// newest release", so an explicit clear event on every connect is pure noise.
func TestResendPersistedAgentUpdateTargetSkipsClearedTargets(t *testing.T) {
	for _, stored := range []string{"", "   "} {
		writer := &recordingAgentUpdateWriter{}
		lookup := func(string) (string, error) { return stored, nil }
		if err := resendPersistedAgentUpdateTarget(writer, "node-1", lookup); err != nil {
			t.Fatalf("re-send with %q: %v", stored, err)
		}
		if len(writer.written) != 0 {
			t.Fatalf("stored %q wrote %+v, want nothing", stored, writer.written)
		}
	}
}

// A lookup failure is reported to the caller (which logs it) and writes nothing.
func TestResendPersistedAgentUpdateTargetReportsLookupFailure(t *testing.T) {
	writer := &recordingAgentUpdateWriter{}
	lookupErr := errors.New("database is closed")
	lookup := func(string) (string, error) { return "", lookupErr }
	err := resendPersistedAgentUpdateTarget(writer, "node-1", lookup)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("error = %v, want the lookup failure", err)
	}
	if len(writer.written) != 0 {
		t.Fatalf("wrote %+v after a lookup failure", writer.written)
	}
}

// A write failure must not be swallowed: the caller logs it, the connection stays.
func TestResendPersistedAgentUpdateTargetReportsWriteFailure(t *testing.T) {
	writeErr := errors.New("broken pipe")
	writer := &recordingAgentUpdateWriter{err: writeErr}
	lookup := func(string) (string, error) { return "v0.1.41", nil }
	err := resendPersistedAgentUpdateTarget(writer, "node-1", lookup)
	if !errors.Is(err, writeErr) {
		t.Fatalf("error = %v, want the write failure", err)
	}
}

// End to end over the real database: a pinned node receives agent.update on
// connect, and once the operator clears the pin it receives nothing.
func TestResendPersistedAgentUpdateTargetUsesThePersistedRecord(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:v2-connect-target?mode=memory&cache=shared"
	const uuid = "client-v2-connect-target"
	if err := dbcore.GetDBInstance().Create(&models.Client{
		UUID: uuid, Name: uuid, Token: "token-" + uuid,
	}).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}

	if err := clients.SetTargetVersion(uuid, "v0.1.40"); err != nil {
		t.Fatalf("pin target: %v", err)
	}
	writer := &recordingAgentUpdateWriter{}
	if err := resendPersistedAgentUpdateTarget(writer, uuid, clients.GetTargetVersion); err != nil {
		t.Fatalf("re-send pinned target: %v", err)
	}
	if len(writer.written) != 1 {
		t.Fatalf("pinned node got %d payloads, want 1", len(writer.written))
	}

	if err := clients.SetTargetVersion(uuid, ""); err != nil {
		t.Fatalf("clear target: %v", err)
	}
	writer = &recordingAgentUpdateWriter{}
	if err := resendPersistedAgentUpdateTarget(writer, uuid, clients.GetTargetVersion); err != nil {
		t.Fatalf("re-send cleared target: %v", err)
	}
	if len(writer.written) != 0 {
		t.Fatalf("cleared node got %+v, want nothing", writer.written)
	}
}
