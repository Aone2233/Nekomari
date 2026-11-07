package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentruntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/connection"
)

func TestPinnedWSResultRequiresCredentialAndValidEnvelope(t *testing.T) {
	conn := &connection.SafeConn{}
	params := []byte(`{"task_id":7,"config_generation":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","instance":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","session":"cccccccccccccccccccccccccccccccc","dispatch_id":"dddddddddddddddddddddddddddddddd","role":"primary","ping_type":"tcp","family":"ipv6","value":1,"value_us":1234}`)
	valid := v2.RawRequest{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyResult, Params: params}
	admitted := func(_ context.Context, fn func() error) error { return fn() }
	retired := func(context.Context, func() error) error { return api.ErrClientCredentialRetired }
	writes := 0
	write := func(_ context.Context, uuid string, got *connection.SafeConn, result v2.PingFamilyResultParams) error {
		writes++
		if uuid != "node" || got != conn || result.TaskID != 7 || result.Family != "ipv6" {
			t.Fatalf("unexpected result: uuid=%s conn=%p result=%+v", uuid, got, result)
		}
		return nil
	}
	if resp := handlePinnedWSResult(t.Context(), "node", conn, valid, retired, write); resp.Error == nil || resp.Error.Code != -32001 || writes != 0 {
		t.Fatalf("retired credential response=%+v writes=%d", resp, writes)
	}
	if resp := handlePinnedWSResult(t.Context(), "node", conn, valid, admitted, write); resp.Error != nil || writes != 1 {
		t.Fatalf("valid response=%+v writes=%d", resp, writes)
	}
	badRequests := []v2.RawRequest{
		{JSONRPC: "1.0", Params: params},
		{JSONRPC: v2.Version, ID: "not-a-notification", Params: params},
		{JSONRPC: v2.Version},
		{JSONRPC: v2.Version, Params: []byte("null")},
		{JSONRPC: v2.Version, Params: []byte(strings.Repeat(" ", 4097) + "{}")},
		{JSONRPC: v2.Version, Params: []byte(`{"unexpected":true}`)},
		{JSONRPC: v2.Version, Params: []byte(`{} {}`)},
	}
	for _, req := range badRequests {
		if resp := handlePinnedWSResult(t.Context(), "node", conn, req, admitted, write); resp.Error == nil || writes != 1 {
			t.Fatalf("malformed request accepted: %+v response=%+v writes=%d", req, resp, writes)
		}
	}
	if resp := handleV2RPCOperation(t.Context(), "node", valid, false, admitted); resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("generic/POST handler accepted pinned result: %+v", resp)
	}
	if resp := handlePinnedWSResult(t.Context(), "node", conn, valid, admitted, func(context.Context, string, *connection.SafeConn, v2.PingFamilyResultParams) error {
		return errors.New("stale dispatch")
	}); resp.Error == nil || resp.Error.Code != -32000 {
		t.Fatalf("stale dispatch accepted: %+v", resp)
	}
}

func TestPinnedWSResultNeverAcceptsUnregisteredOrReplacedSocket(t *testing.T) {
	const marker = "NEKOMARI_PINNED_RESULT_FENCE_CHILD"
	if os.Getenv(marker) != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPinnedWSResultNeverAcceptsUnregisteredOrReplacedSocket$")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), marker+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated result fence: %v\n%s", err, out)
		}
		return
	}
	const uuid = "pinned-result-network-no-dispatch"
	a, b := &connection.SafeConn{}, &connection.SafeConn{}
	db := dbcore.GetDBInstance()
	task := models.PingTask{Name: "unregistered-result", Type: "tcp", Target: "example.test:80", Interval: 60,
		TargetFamily: "ipv6", PinnedGeneration: strings.Repeat("a", 32), Clients: models.StringArray{uuid}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	agentruntime.SetConnectedClients(uuid, a)
	t.Cleanup(func() { agentruntime.ResetClient(uuid) })
	result := v2.PingFamilyResultParams{TaskID: task.Id, ConfigGeneration: strings.Repeat("a", 32), Instance: strings.Repeat("b", 32),
		Session: strings.Repeat("c", 32), DispatchID: strings.Repeat("d", 32), Role: "primary", PingType: "tcp", Family: "ipv6"}
	value, micros := 1, int64(1234)
	result.Value, result.ValueUS = &value, &micros
	params, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	req := v2.RawRequest{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyResult, Params: params}
	current := func(_ context.Context, fn func() error) error {
		if agentruntime.GetConnectedClient(uuid) != a {
			return api.ErrClientCredentialRetired
		}
		return fn()
	}
	if resp := handlePinnedWSResult(t.Context(), uuid, a, req, current, agentruntime.AdmitPinnedResult); resp.Error == nil {
		t.Fatal("unregistered result was accepted")
	}
	agentruntime.SetConnectedClients(uuid, b)
	if resp := handlePinnedWSResult(t.Context(), uuid, a, req, current, agentruntime.AdmitPinnedResult); resp.Error == nil || resp.Error.Code != -32001 {
		t.Fatalf("replaced socket result accepted: %+v", resp)
	}
}
