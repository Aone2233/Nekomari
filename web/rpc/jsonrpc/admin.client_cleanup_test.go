package jsonrpc

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentrt "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestAdminRemoveClientCleansEventQueueOnlyAfterDatabaseSuccess(t *testing.T) {
	db := dbcore.GetDBInstance()
	for _, route := range []string{"rpc-handler", "rest-bridge", "database-failure"} {
		t.Run(route, func(t *testing.T) {
			id := uuid.NewString()
			if err := db.Create(&models.Client{UUID: id, Token: "test-only-cleanup-" + id}).Error; err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				agentrt.DeleteV2EventQueue(id)
				agentrt.DeleteLatestReport(id)
				agentrt.DeleteConnectedClients(id)
				db.Delete(&models.Client{}, "uuid = ?", id)
			})
			agentrt.MarkV2Client(id)
			agentrt.RecordReport(v2.Report{UUID: id})
			agentrt.NoteReportClamp(id, "ram")
			event := agentrt.EnqueueV2Event(id, "agent.exec", "test-only-command")
			if route == "database-failure" {
				if err := db.Exec("CREATE TRIGGER reject_cleanup_delete BEFORE DELETE ON clients WHEN OLD.uuid = '" + id + "' BEGIN SELECT RAISE(ABORT, 'test-only-delete-failure'); END").Error; err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Exec("DROP TRIGGER reject_cleanup_delete") })
				_, jerr := adminRemoveClient(t.Context(), &rpc.JsonRpcRequest{Params: map[string]any{"uuid": id}})
				if jerr == nil {
					t.Fatal("database failure was not reported")
				}
				if events := agentrt.TakeV2Events(id, nil, 0); len(events) != 1 || events[0].ID != event.ID ||
					agentrt.GetLatestReport(id)[id] == nil || !agentrt.IsV2Client(id) || agentrt.NoteReportClamp(id, "ram") {
					t.Fatal("failed DB deletion cleared valid runtime/queue state")
				}
				return
			}
			done := make(chan []v2.Event, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				done <- agentrt.WaitV2Events(id, []string{event.ID}, time.Minute)
			}()
			t.Cleanup(func() {
				agentrt.DeleteV2EventQueue(id)
				select {
				case <-finished:
				case <-time.After(2 * time.Second):
					t.Error("deleted node poll did not exit")
				}
			})
			deadline := time.Now().Add(2 * time.Second)
			for len(agentrt.TakeV2Events(id, nil, 0)) != 0 {
				if time.Now().After(deadline) {
					t.Fatal("long poll did not acknowledge the seed")
				}
				time.Sleep(time.Millisecond)
			}
			if route == "rest-bridge" {
				engine := gin.New()
				engine.Use(func(c *gin.Context) { api.SetPrincipal(c, rpc.NewUserPrincipal("test-only-cleanup-admin")) })
				engine.POST("/api/admin/client/:uuid/remove", Bind("admin:removeClient", WithPath("uuid")))
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/client/"+id+"/remove", nil))
				if w.Code != http.StatusOK {
					t.Fatalf("REST removal status = %d", w.Code)
				}
			} else if _, jerr := adminRemoveClient(t.Context(), &rpc.JsonRpcRequest{Params: map[string]any{"uuid": id}}); jerr != nil {
				t.Fatal(jerr)
			}
			select {
			case events := <-done:
				if len(events) != 0 {
					t.Fatal("deleted node poll returned commands")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("successful deletion did not wake long poll")
			}
			var count int64
			if err := db.Model(&models.Client{}).Where("uuid = ?", id).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("client row survived successful deletion")
			}
			if events := agentrt.TakeV2Events(id, nil, 0); len(events) != 0 || agentrt.GetLatestReport(id)[id] != nil ||
				agentrt.IsV2Client(id) || !agentrt.NoteReportClamp(id, "ram") {
				t.Fatal("successful deletion retained runtime/queue/anomaly state")
			}
		})
	}
}
