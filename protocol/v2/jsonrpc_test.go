package v2

import (
	"encoding/json"
	"strings"
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

// PingResultParams 必须认得新 agent 的 value_us，同时旧 payload 仍然可用。
//
// 这个字段是 agent 与面板之间唯一的协议改动：旧 agent 不发它（值为 0），面板回退到
// 整数毫秒的 value；新 agent 发它，面板写小数毫秒 —— 两种 payload 都必须能解码。
func TestPingResultParamsCarriesMicroseconds(t *testing.T) {
	decode := func(body string) PingResultParams {
		t.Helper()
		var req RawRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal raw request: %v", err)
		}
		var params PingResultParams
		if err := req.DecodeParams(&params); err != nil {
			t.Fatalf("decode params: %v", err)
		}
		return params
	}

	modern := decode(`{"jsonrpc":"2.0","method":"agent.pingResult","params":{"task_id":7,"ping_type":"icmp","value":0,"value_us":420}}`)
	if modern.TaskID != 7 || modern.PingType != "icmp" || modern.Value != 0 || modern.ValueUS != 420 {
		t.Fatalf("decoded microsecond params = %+v", modern)
	}

	legacy := decode(`{"jsonrpc":"2.0","method":"agent.pingResult","params":{"task_id":7,"ping_type":"icmp","value":5}}`)
	if legacy.Value != 5 || legacy.ValueUS != 0 {
		t.Fatalf("legacy params = %+v, want value 5 and no value_us", legacy)
	}

	// 0 在 JSON 里省略（omitempty）：旧 agent 的响应形状因此完全不变。
	encodedLegacy, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy params: %v", err)
	}
	if strings.Contains(string(encodedLegacy), "value_us") {
		t.Errorf("a zero value_us was encoded: %s", encodedLegacy)
	}
	encodedModern, err := json.Marshal(modern)
	if err != nil {
		t.Fatalf("marshal modern params: %v", err)
	}
	if !strings.Contains(string(encodedModern), `"value_us":420`) {
		t.Errorf("the microsecond value is missing from %s", encodedModern)
	}
}
