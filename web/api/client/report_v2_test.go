package client

import (
	"encoding/json"
	"testing"

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
