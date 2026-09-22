package v2

import (
	"encoding/json"
	"testing"
	"time"
)

// TestRawRequestDecodeParamsTypes verifies that ingest decodes the typed params
// directly from the bytes that arrived.
func TestRawRequestDecodeParamsTypes(t *testing.T) {
	body := []byte(`{
		"jsonrpc": "2.0",
		"method": "agent.report",
		"params": {
			"report": {
				"uuid": "node-1",
				"cpu": {"name": "x", "cores": 4, "usage": 12.5},
				"ram": {"total": 1024, "used": 512},
				"uptime": 123456,
				"updated_at": "2026-09-22T10:00:00Z"
			},
			"ack_event_ids": ["evt-1", "evt-2"]
		},
		"id": 7
	}`)

	var req RawRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal raw request: %v", err)
	}
	if req.JSONRPC != Version || req.Method != MethodAgentReport {
		t.Fatalf("envelope = %q/%q", req.JSONRPC, req.Method)
	}
	if len(req.Params) == 0 {
		t.Fatal("params were not retained as raw JSON")
	}

	var params ReportParams
	if err := req.DecodeParams(&params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Report.UUID != "node-1" || params.Report.CPU.Cores != 4 || params.Report.CPU.Usage != 12.5 {
		t.Fatalf("decoded report = %+v", params.Report)
	}
	if params.Report.Uptime != 123456 || params.Report.Ram.Used != 512 {
		t.Fatalf("decoded report numbers = %+v", params.Report)
	}
	if !params.Report.UpdatedAt.Equal(time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("decoded updated_at = %s", params.Report.UpdatedAt)
	}
	if len(params.AckEventIDs) != 2 || params.AckEventIDs[0] != "evt-1" {
		t.Fatalf("decoded ack ids = %v", params.AckEventIDs)
	}
}

// TestRawRequestDecodeParamsIsExact pins the reason the params no longer travel
// through an untyped map: every JSON number in that map is a float64, so an
// integer counter above 2^53 (a byte total, for instance) came back one off
// after the map was marshalled again.
func TestRawRequestDecodeParamsIsExact(t *testing.T) {
	const exact int64 = 9007199254740993 // 2^53 + 1
	body := []byte(`{"jsonrpc":"2.0","method":"agent.report","params":{"total_up":` + "9007199254740993" + `}}`)

	var req RawRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal raw request: %v", err)
	}
	var typed struct {
		TotalUp int64 `json:"total_up"`
	}
	if err := req.DecodeParams(&typed); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if typed.TotalUp != exact {
		t.Fatalf("decoded total_up = %d, want %d", typed.TotalUp, exact)
	}
}

// TestRawRequestDecodeParamsOptional verifies that an omitted or null params
// object leaves the method defaults alone instead of failing the request.
func TestRawRequestDecodeParamsOptional(t *testing.T) {
	target := PullParams{Capabilities: []string{"default"}}

	for name, raw := range map[string]json.RawMessage{
		"absent": nil,
		"null":   json.RawMessage(`null`),
		"spaced": json.RawMessage("  null \n"),
	} {
		req := RawRequest{JSONRPC: Version, Method: MethodAgentPull, Params: raw}
		if err := req.DecodeParams(&target); err != nil {
			t.Fatalf("%s: decode params: %v", name, err)
		}
		if len(target.Capabilities) != 1 || target.Capabilities[0] != "default" {
			t.Fatalf("%s: target was modified: %+v", name, target)
		}
	}
}

func TestRawRequestDecodeParamsRejectsMalformed(t *testing.T) {
	req := RawRequest{JSONRPC: Version, Method: MethodAgentPull, Params: json.RawMessage(`{"ack_event_ids":`)}
	var params PullParams
	if err := req.DecodeParams(&params); err == nil {
		t.Fatal("expected malformed params to fail")
	}
}

// TestRequestStillCarriesTypedParams pins the other half of the protocol: the
// shape the panel uses to *build* a message keeps taking Go values, because its
// params are marshalled onto the wire rather than decoded from it.
func TestRequestStillCarriesTypedParams(t *testing.T) {
	payload, err := json.Marshal(Request{
		JSONRPC: Version,
		Method:  MethodAgentExec,
		Params:  ExecParams{TaskID: "task-1", Command: "uptime"},
		ID:      "1",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded struct {
		Params ExecParams `json:"params"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if decoded.Params.TaskID != "task-1" || decoded.Params.Command != "uptime" {
		t.Fatalf("decoded params = %+v", decoded.Params)
	}
}
