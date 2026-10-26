package jsonrpc

import (
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

func TestRetiredAPIKeyCannotBeImportedOrExported(t *testing.T) {
	for _, handler := range []rpc.Handler{adminPlanConfigImport, adminImportConfig} {
		result, jerr := handler(t.Context(), &rpc.JsonRpcRequest{Params: map[string]any{
			"document": configxport.Document{SchemaVersion: configxport.SchemaVersion,
				Settings: map[string]any{config.ApiKeyKey: "test-only-retired-key"}},
		}})
		if result != nil || jerr == nil || jerr.Code != rpc.InvalidParams {
			t.Fatal("retired credential import was not rejected before planning/applying")
		}
	}
	for _, create := range []bool{false, true} {
		if err := applyRecord("settings", "", map[string]any{"key": config.ApiKeyKey, "value": "test-only-retired-key"}, create); err == nil {
			t.Fatal("direct import applier accepted a retired key")
		}
	}
	if err := config.Set(config.ApiKeyKey, "test-only-retired-key"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Database().Delete(&config.ConfigItem{}, "key = ?", config.ApiKeyKey).Error })
	for _, includeSecrets := range []bool{false, true} {
		settings, err := (exportStore{}).ExportSettings(includeSecrets)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := settings[config.ApiKeyKey]; exists {
			t.Fatal("configuration export included a retired global credential")
		}
	}
	result, jerr := adminGetSettings(t.Context(), nil)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if _, exists := result.(map[string]any)[config.ApiKeyKey]; exists {
		t.Fatal("settings read included a retired global credential")
	}
}
