package client

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
)

// 上报值到记录的翻译层：value_us 原样带下去，旧 agent（无 value_us）保持 0。
//
// 落库/聚合那一侧（小数毫秒、loss 判定）由 internal/metricstore 的用例覆盖；这里钉住的
// 是「协议字段没有被入口丢掉」——report_v2 把 params.ValueUS 交给 ingestPingResult，
// ingestPingResult 再交给 newPingRecord。签名是编译期保证，字段映射由这条用例保证。
func TestNewPingRecordCarriesMicroseconds(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

	modern := newPingRecord("node-a", 7, "icmp", "reference", "ipv6", 5, 420, at)
	if modern.Client != "node-a" || modern.TaskId != 7 || modern.PingType != "icmp" ||
		modern.Role != "reference" || modern.Family != "ipv6" {
		t.Fatalf("identity fields changed: %+v", modern)
	}
	if modern.Value != 5 {
		t.Errorf("legacy value = %d, want 5: old readers must keep working", modern.Value)
	}
	if modern.ValueUS != 420 {
		t.Errorf("value_us = %d, want the reported 420 microseconds", modern.ValueUS)
	}
	if !modern.Time.Equal(at) {
		t.Errorf("time = %s, want %s", modern.Time, at)
	}

	legacy := newPingRecord("node-a", 7, "icmp", "", "", 5, 0, at)
	if legacy.ValueUS != 0 {
		t.Errorf("an old agent produced value_us = %d, want 0", legacy.ValueUS)
	}

	// JSON 契约：0 省略（旧响应形状不变），非 0 是整数微秒。
	encodedLegacy, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy record: %v", err)
	}
	if strings.Contains(string(encodedLegacy), "value_us") {
		t.Errorf("a zero value_us was encoded: %s", encodedLegacy)
	}
	encodedModern, err := json.Marshal(modern)
	if err != nil {
		t.Fatalf("marshal modern record: %v", err)
	}
	if !strings.Contains(string(encodedModern), "\"value_us\":420") {
		t.Errorf("value_us is missing from %s", encodedModern)
	}
	var decoded models.PingRecord
	if err := json.Unmarshal(encodedModern, &decoded); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if decoded.ValueUS != 420 {
		t.Errorf("round-trip value_us = %d, want 420", decoded.ValueUS)
	}
}
