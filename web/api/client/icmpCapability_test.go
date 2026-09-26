package client

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

// What the agent reports about its ICMP sockets has to survive the whole path:
// the v2 basic-info notification, the ingest, and the column. This drives the
// real RPC handler rather than calling the save function, because the key is a
// free-form map entry and nothing but the round trip proves the column matches.
func TestV2BasicInfoStoresICMPCapability(t *testing.T) {
	flags.DatabaseType = "sqlite"
	flags.DatabaseFile = "file:v2_icmp_capability?mode=memory&cache=shared"

	db := dbcore.GetDBInstance()
	now := time.Now().UTC()

	cases := []struct {
		name     string
		reported string
		want     string
	}{
		{"raw socket", "raw", "raw"},
		{"ping socket fallback", "ping", "ping"},
		{"neither socket", "none", "none"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uuid := "client-icmp-" + tc.reported
			if err := db.Create(&models.Client{
				UUID:      uuid,
				Token:     "token-" + tc.reported,
				Name:      "client_" + tc.reported,
				CreatedAt: now,
				UpdatedAt: now,
			}).Error; err != nil {
				t.Fatalf("create client: %v", err)
			}

			params, _ := json.Marshal(map[string]interface{}{
				"info": map[string]interface{}{
					"ipv4":            "203.0.113.7",
					"icmp_capability": tc.reported,
				},
			})
			resp := handleV2RPC(uuid, v2.RawRequest{
				JSONRPC: v2.Version,
				Method:  v2.MethodAgentBasicInfo,
				Params:  params,
				ID:      "basic-info",
			}, false)
			if resp.Error != nil {
				t.Fatalf("v2 basic info failed: %+v", resp.Error)
			}

			var got models.Client
			if err := db.First(&got, "uuid = ?", uuid).Error; err != nil {
				t.Fatalf("load client: %v", err)
			}
			if got.ICMPCapability != tc.want {
				t.Fatalf("ICMPCapability = %q, want %q", got.ICMPCapability, tc.want)
			}
		})
	}
}

// An older agent does not send the key at all, and the column has to stay empty.
// The panel reads empty as "unknown"; if a missing key landed as "none", every
// node that has not been upgraded yet would be drawn as unable to probe, and an
// ICMP task on it would look like a broken target.
func TestV2BasicInfoLeavesICMPCapabilityEmptyWhenAbsent(t *testing.T) {
	flags.DatabaseType = "sqlite"
	flags.DatabaseFile = "file:v2_icmp_absent?mode=memory&cache=shared"

	db := dbcore.GetDBInstance()
	now := time.Now().UTC()
	const uuid = "client-icmp-absent"
	if err := db.Create(&models.Client{
		UUID:      uuid,
		Token:     "token-absent",
		Name:      "client_absent",
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}

	// The shape an older agent sends: no icmp_capability key at all.
	resp := handleV2RPC(uuid, v2.RawRequest{
		JSONRPC: v2.Version,
		Method:  v2.MethodAgentBasicInfo,
		Params:  json.RawMessage(`{"info":{"ipv4":"203.0.113.8"}}`),
		ID:      "basic-info",
	}, false)
	if resp.Error != nil {
		t.Fatalf("v2 basic info failed: %+v", resp.Error)
	}

	var got models.Client
	if err := db.First(&got, "uuid = ?", uuid).Error; err != nil {
		t.Fatalf("load client: %v", err)
	}
	if got.ICMPCapability != "" {
		t.Fatalf("ICMPCapability = %q, want empty (unknown)", got.ICMPCapability)
	}
}
