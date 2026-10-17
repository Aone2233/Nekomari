package ipinfo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMintNetworkSummaryRedactsAddressAndRejectsPrivate(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	if _, err := handler.NodeNetworkSummary(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("private IP accepted")
	}
	if upstreams.hitsFor("geo") != 0 {
		t.Fatal("invalid address reached upstream")
	}
	result, err := handler.NodeNetworkSummary(context.Background(), "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "1.2.3.4") || strings.Contains(string(encoded), "latitude") {
		t.Fatalf("private details leaked: %s", encoded)
	}
	if result.Source == "" || result.Country == "" {
		t.Fatalf("evidence missing: %+v", result)
	}
}
