package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/gin-gonic/gin"
)

// Run in a disposable network namespace with loopback only. These are actual
// market plugins, using synthetic credentials and a local HTTP receiver.
func TestKomariMarketHTTPNotificationDelivery(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	cases := []struct {
		name, id, path string
		config         func(string) map[string]any
	}{
		{"bark-plus", "bark", "/push", func(base string) map[string]any {
			return map[string]any{"server_url": base, "device_key": "test-device"}
		}},
		{"gotify-notification-plugin", "gotify", "/message", func(base string) map[string]any { return map[string]any{"server_url": base, "token": "test-token"} }},
		{"serverchan3-notification-plugin", "Server酱³", "/send", func(base string) map[string]any { return map[string]any{"api_url": base + "/send"} }},
		{"serverchanturbo-notification-plugin", "Server酱Turbo", "/send", func(base string) map[string]any { return map[string]any{"api_url": base + "/send"} }},
		{"telegram-notification-plugin", "telegram", "/bottest-token/sendMessage", func(base string) map[string]any {
			return map[string]any{"endpoint": base + "/bot", "bot_token": "test-token", "chat_id": "123"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archive := verifiedMarketPluginArchive(t, dir, tc.name)
			received := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err == nil && (r.Method != "POST" || r.URL.Path != tc.path || !strings.Contains(string(raw), "hello")) {
					err = fmt.Errorf("unexpected request: %s %s %q", r.Method, r.URL.Path, raw)
				}
				received <- err
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":200,"ok":true,"message":"success"}`))
			}))
			defer server.Close()
			withTempDataDir(t)
			Init(gin.New())
			if err := dbcore.GetDBInstance().AutoMigrate(&models.MessageSenderProvider{}); err != nil {
				t.Fatal(err)
			}
			old := utils.CurrentVersion
			utils.CurrentVersion = "1.7.30"
			t.Cleanup(func() { utils.CurrentVersion = old })
			if _, err := InstallZip(archive); err != nil {
				t.Fatal(err)
			}
			if err := SetEnabled(tc.name, true, true); err != nil {
				t.Fatal(err)
			}
			defer SetEnabled(tc.name, false, false)
			id, ok := messageSender.ChannelSelectionID(tc.id)
			if !ok {
				t.Fatalf("channel %s not registered", tc.id)
			}
			raw, _ := json.Marshal(tc.config(server.URL))
			if err := database.SaveMessageSenderConfig(&models.MessageSenderProvider{Name: id, Addition: string(raw)}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			found, err := messageSender.CallNotificationChannel(ctx, id, messageSender.Notification{Title: "test", Message: "hello"})
			if !found || err != nil {
				t.Fatalf("delivery: %v %v", found, err)
			}
			select {
			case err := <-received:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("no local request")
			}
		})
	}
}
