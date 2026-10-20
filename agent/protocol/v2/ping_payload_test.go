package v2

import (
	"encoding/json"
	"testing"
	"time"
)

// decodePingResult 把一条 ping 结果通知解成方法名与参数表，便于断言线缆形状
// （包括"某个字段是否真的不存在"，这在校验旧面板兼容时是关键）。
func decodePingResult(t *testing.T, payload interface{}) (string, map[string]interface{}) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal ping payload: %v", err)
	}
	var wire struct {
		JSONRPC string                 `json:"jsonrpc"`
		Method  string                 `json:"method"`
		Params  map[string]interface{} `json:"params"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal ping payload: %v", err)
	}
	if wire.JSONRPC != Version {
		t.Fatalf("jsonrpc = %q, want %q", wire.JSONRPC, Version)
	}
	return wire.Method, wire.Params
}

// 旧构建函数必须保持旧的线缆形状：没有 value_us。旧面板只认识 value（毫秒），
// 而"保持旧签名 = 保持旧形状"是这里唯一能接受的解释。
func TestLegacyPingPayloadHasNoMicrosecondField(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	method, params := decodePingResult(t, BuildPingResultPayloadWithRoleAndFamily(7, "icmp", "reference", "ipv6", 12, at))

	if method != MethodAgentPingResult {
		t.Fatalf("method = %q, want %q", method, MethodAgentPingResult)
	}
	if _, ok := params["value_us"]; ok {
		t.Fatal("the legacy builder wrote value_us; old-panel-compatible payloads must not change shape")
	}
	if got := params["value"]; got != float64(12) {
		t.Fatalf("value = %v, want 12 (milliseconds)", got)
	}
	if params["role"] != "reference" || params["family"] != "ipv6" {
		t.Fatalf("role/family = %v/%v, want reference/ipv6", params["role"], params["family"])
	}
	if params["finished_at"] != at.Format(time.RFC3339Nano) {
		t.Fatalf("finished_at = %v", params["finished_at"])
	}
}

// 真实测量路径用带微秒的构建函数：value 仍是毫秒（旧面板），value_us 是精度来源。
func TestMicrosecondPingPayloadCarriesBothFields(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	method, params := decodePingResult(t, BuildPingResultPayloadWithRoleFamilyAndMicros(9, "tcp", "", "ipv4", 1420, 1_420_000, at))

	if method != MethodAgentPingResult {
		t.Fatalf("method = %q, want %q", method, MethodAgentPingResult)
	}
	if got := params["value"]; got != float64(1420) {
		t.Fatalf("value = %v, want 1420 (milliseconds, for old panels)", got)
	}
	if got := params["value_us"]; got != float64(1_420_000) {
		t.Fatalf("value_us = %v, want 1420000", got)
	}
	if _, ok := params["role"]; ok {
		t.Fatal("an empty role must not be written")
	}
	if params["family"] != "ipv4" {
		t.Fatalf("family = %v, want ipv4", params["family"])
	}
	if params["task_id"] != float64(9) || params["ping_type"] != "tcp" {
		t.Fatalf("task/type = %v/%v", params["task_id"], params["ping_type"])
	}
}

// 失败（丢包）时两个字段都是 -1：面板的丢包判据是 Value<0，微秒字段跟同一个哨兵，
// 这样"没连上"在任何一侧都只有一种表示。
func TestMicrosecondPingPayloadKeepsTheLossSentinel(t *testing.T) {
	_, params := decodePingResult(t, BuildPingResultPayloadWithRoleFamilyAndMicros(11, "icmp", "", "", -1, -1, time.Now()))

	if got := params["value"]; got != float64(-1) {
		t.Fatalf("value = %v, want -1", got)
	}
	if got := params["value_us"]; got != float64(-1) {
		t.Fatalf("value_us = %v, want -1", got)
	}
}

// 最短的两个构建函数同样保持旧形状（它们的调用方不关心 role/family/微秒）。
func TestShortPingBuildersKeepTheOldShape(t *testing.T) {
	for _, payload := range []interface{}{
		BuildPingResultPayload(1, "icmp", 5, time.Now()),
		BuildPingResultPayloadWithRole(2, "icmp", "reference", 6, time.Now()),
	} {
		_, params := decodePingResult(t, payload)
		if _, ok := params["value_us"]; ok {
			t.Fatal("value_us leaked into a legacy builder")
		}
		if _, ok := params["value"]; !ok {
			t.Fatal("the legacy builder dropped value")
		}
	}
}
