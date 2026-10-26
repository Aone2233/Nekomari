package client

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/api"
)

func TestRetiredHandshakeCannotSettlePendingRolloutClear(t *testing.T) {
	retired := false
	operation := func(_ context.Context, fn func() error) error {
		if retired {
			return api.ErrClientCredentialRetired
		}
		return fn()
	}
	writer := retireOnWrite{retire: func() { retired = true }}
	cleared := false
	err := resendPersistedAgentUpdateTargetOperation(t.Context(), operation, writer, "retired-handshake",
		func(string) (string, bool, error) { return "", true, nil },
		func(string, bool) error { cleared = true; return nil })
	if !errors.Is(err, api.ErrClientCredentialRetired) || cleared {
		t.Fatal("retired handshake settled new-generation rollout state")
	}
}

type retireOnWrite struct{ retire func() }

func (w retireOnWrite) WriteJSON(any) error { w.retire(); return nil }

func TestAllAgentMutationsUseCredentialAdmission(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:admission-methods?mode=memory&cache=shared"
	config.SetDb(dbcore.GetDBInstance())
	for method, params := range map[string]string{
		v2.MethodAgentReport:     `{"report":{"cpu":{"usage":1}}}`,
		v2.MethodAgentBasicInfo:  `{"info":{"cpu_name":"stale"}}`,
		v2.MethodAgentUnlock:     `{}`,
		v2.MethodAgentPingResult: `{"task_id":1,"value":1}`,
		v2.MethodAgentTaskResult: `{"task_id":"fixture","result":"stale"}`,
		v2.MethodAgentPull:       `{}`,
		v2.MethodAgentFileResult: `{"request_id":"fixture","ok":true}`,
	} {
		t.Run(method, func(t *testing.T) {
			admissions := 0
			response := handleV2RPCOperation(t.Context(), "retired-mutations", v2.RawRequest{JSONRPC: v2.Version,
				Method: method, Params: json.RawMessage(params), ID: 1}, false,
				func(context.Context, func() error) error { admissions++; return api.ErrClientCredentialRetired })
			if admissions != 1 || response.Error == nil || response.Error.Code != -32001 {
				t.Fatal("agent method bypassed credential retirement")
			}
		})
	}
}

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

// recordingPendingClear stands in for clients.SetTargetVersionClearPending.
type recordingPendingClear struct {
	pending []bool
	err     error
}

func (r *recordingPendingClear) clear(uuid string, pending bool) error {
	if r.err != nil {
		return r.err
	}
	r.pending = append(r.pending, pending)
	return nil
}

// stateLookup returns a lookup function with one fixed answer.
func stateLookup(target string, pendingClear bool, err error) func(string) (string, bool, error) {
	return func(string) (string, bool, error) { return target, pendingClear, err }
}

// agentUpdateTargetOf extracts the target a recorded payload carries.
func agentUpdateTargetOf(t *testing.T, written []any, index int) string {
	t.Helper()
	if index >= len(written) {
		t.Fatalf("wrote %d payloads, want at least %d", len(written), index+1)
	}
	request, ok := written[index].(v2.Request)
	if !ok {
		t.Fatalf("payload = %T, want v2.Request", written[index])
	}
	if request.JSONRPC != v2.Version || request.Method != v2.MethodAgentUpdate {
		t.Fatalf("payload = %+v, want a %s request", request, v2.MethodAgentUpdate)
	}
	params, ok := request.Params.(v2.UpdateParams)
	if !ok {
		t.Fatalf("params = %T, want v2.UpdateParams", request.Params)
	}
	return params.Target
}

// The connect-time re-send is what makes a rollout survive an agent restart: the
// agent keeps the target in memory only, so a node that reconnects must be told
// again. This pins the exact event the panel sends.
func TestResendPersistedAgentUpdateTargetSendsThePinnedVersion(t *testing.T) {
	writer := &recordingAgentUpdateWriter{}
	cleared := &recordingPendingClear{}
	lookup := func(uuid string) (string, bool, error) {
		if uuid != "node-1" {
			t.Fatalf("lookup uuid = %q", uuid)
		}
		return "v0.1.41", false, nil
	}
	if err := resendPersistedAgentUpdateTarget(writer, "node-1", lookup, cleared.clear); err != nil {
		t.Fatalf("re-send: %v", err)
	}
	if got := agentUpdateTargetOf(t, writer.written, 0); got != "v0.1.41" {
		t.Fatalf("target = %q, want v0.1.41", got)
	}
	if len(cleared.pending) != 0 {
		t.Fatalf("a pinned target touched the pending marker: %v", cleared.pending)
	}
}

// No pin and nothing pending is the normal fleet: send nothing, at all.
func TestResendPersistedAgentUpdateTargetSkipsClearedTargets(t *testing.T) {
	for _, stored := range []string{"", "   "} {
		writer := &recordingAgentUpdateWriter{}
		cleared := &recordingPendingClear{}
		if err := resendPersistedAgentUpdateTarget(writer, "node-1", stateLookup(stored, false, nil), cleared.clear); err != nil {
			t.Fatalf("re-send with %q: %v", stored, err)
		}
		if len(writer.written) != 0 {
			t.Fatalf("stored %q wrote %+v, want nothing", stored, writer.written)
		}
		if len(cleared.pending) != 0 {
			t.Fatalf("stored %q touched the pending marker: %v", stored, cleared.pending)
		}
	}
}

// A clear that never reached the node is an explicit empty target on connect, and
// the marker is retired only after the message is actually written.
func TestResendPersistedAgentUpdateTargetDeliversAPendingClear(t *testing.T) {
	writer := &recordingAgentUpdateWriter{}
	cleared := &recordingPendingClear{}
	if err := resendPersistedAgentUpdateTarget(writer, "node-1", stateLookup("", true, nil), cleared.clear); err != nil {
		t.Fatalf("re-send: %v", err)
	}
	if got := agentUpdateTargetOf(t, writer.written, 0); got != "" {
		t.Fatalf("target = %q, want the explicit empty target", got)
	}
	if len(cleared.pending) != 1 || cleared.pending[0] {
		t.Fatalf("pending clear calls = %v, want exactly one false", cleared.pending)
	}
}

// A lookup failure is reported to the caller (which logs it) and writes nothing.
func TestResendPersistedAgentUpdateTargetReportsLookupFailure(t *testing.T) {
	writer := &recordingAgentUpdateWriter{}
	cleared := &recordingPendingClear{}
	lookupErr := errors.New("database is closed")
	err := resendPersistedAgentUpdateTarget(writer, "node-1", stateLookup("", false, lookupErr), cleared.clear)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("error = %v, want the lookup failure", err)
	}
	if len(writer.written) != 0 {
		t.Fatalf("wrote %+v after a lookup failure", writer.written)
	}
	if len(cleared.pending) != 0 {
		t.Fatalf("cleared the marker after a lookup failure: %v", cleared.pending)
	}
}

// A write failure must not be swallowed, and it must not retire the pending clear:
// the debt is only paid when the node has actually been told.
func TestResendPersistedAgentUpdateTargetReportsWriteFailure(t *testing.T) {
	for _, state := range []struct {
		name    string
		target  string
		pending bool
	}{
		{"pinned", "v0.1.41", false},
		{"pending_clear", "", true},
	} {
		t.Run(state.name, func(t *testing.T) {
			writeErr := errors.New("broken pipe")
			writer := &recordingAgentUpdateWriter{err: writeErr}
			cleared := &recordingPendingClear{}
			err := resendPersistedAgentUpdateTarget(writer, "node-1", stateLookup(state.target, state.pending, nil), cleared.clear)
			if !errors.Is(err, writeErr) {
				t.Fatalf("error = %v, want the write failure", err)
			}
			if len(cleared.pending) != 0 {
				t.Fatalf("cleared the marker even though the write failed: %v", cleared.pending)
			}
		})
	}
}

// Failing to record the delivery is reported to the caller, but the message has
// already gone out: the next connect would send the clear again, which is safe.
func TestResendPersistedAgentUpdateTargetReportsClearFailure(t *testing.T) {
	writer := &recordingAgentUpdateWriter{}
	clearErr := errors.New("database is read-only")
	cleared := &recordingPendingClear{err: clearErr}
	err := resendPersistedAgentUpdateTarget(writer, "node-1", stateLookup("", true, nil), cleared.clear)
	if !errors.Is(err, clearErr) {
		t.Fatalf("error = %v, want the clear failure", err)
	}
	if len(writer.written) != 1 {
		t.Fatalf("wrote %d payloads, want the clear to have been sent", len(writer.written))
	}
}

// End to end over the real database: a pinned node receives agent.update on
// connect; a clear that arrived while it was offline is delivered once and the
// marker then goes away; a clear that was already delivered sends nothing.
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
	if err := resendPersistedAgentUpdateTarget(writer, uuid, clients.AgentUpdateState, clients.SetTargetVersionClearPending); err != nil {
		t.Fatalf("re-send pinned target: %v", err)
	}
	if len(writer.written) != 1 {
		t.Fatalf("pinned node got %d payloads, want 1", len(writer.written))
	}

	// Cleared while offline: the marker is the only thing that makes the next
	// connect tell the node, and it must be retired once it has.
	if err := clients.SetAgentUpdateState(uuid, "", true); err != nil {
		t.Fatalf("record pending clear: %v", err)
	}
	writer = &recordingAgentUpdateWriter{}
	if err := resendPersistedAgentUpdateTarget(writer, uuid, clients.AgentUpdateState, clients.SetTargetVersionClearPending); err != nil {
		t.Fatalf("deliver pending clear: %v", err)
	}
	if len(writer.written) != 1 {
		t.Fatalf("pending clear wrote %d payloads, want 1", len(writer.written))
	}
	if got := agentUpdateTargetOf(t, writer.written, 0); got != "" {
		t.Fatalf("pending clear target = %q, want empty", got)
	}
	target, pending, err := clients.AgentUpdateState(uuid)
	if err != nil {
		t.Fatalf("read settled state: %v", err)
	}
	if target != "" || pending {
		t.Fatalf("state after delivery = (%q, %v), want empty and settled", target, pending)
	}

	// The debt is paid: a second connect sends nothing again.
	writer = &recordingAgentUpdateWriter{}
	if err := resendPersistedAgentUpdateTarget(writer, uuid, clients.AgentUpdateState, clients.SetTargetVersionClearPending); err != nil {
		t.Fatalf("re-send settled state: %v", err)
	}
	if len(writer.written) != 0 {
		t.Fatalf("settled node got %+v, want nothing", writer.written)
	}
}
