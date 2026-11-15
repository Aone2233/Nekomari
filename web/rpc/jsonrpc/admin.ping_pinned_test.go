package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestDisabledPinnedCallbacksAndImportFailClosed(t *testing.T) {
	_, addErr := adminAddPingTaskWith(t.Context(), &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: "admin:addPingTask", Params: map[string]any{
		"name": "test", "target": "example.com:80", "type": "tcp", "interval": 60, "clients": []string{"node"}, "target_family": "ipv6",
	}}, nil)
	if addErr == nil || addErr.Code != rpc.InvalidParams {
		t.Fatalf("draft pinned creation accepted: %+v", addErr)
	}
	db := dbcore.GetDBInstance()
	task := models.PingTask{Name: "draft-pinned", Type: "tcp", Target: "example.com:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"node"}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	_, editErr := adminEditPingTaskWith(&rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: "admin:editPingTask", Params: map[string]any{
		"tasks": []map[string]any{{"id": task.Id, "name": "changed", "target": "example.com:80", "type": "tcp", "interval": 60, "clients": []string{"node"}, "target_family": "ipv6"}},
	}}, false, tasks.EditPingTask)
	if editErr == nil {
		t.Fatal("legacy edit changed draft pinned task")
	}
	var after models.PingTask
	if err := db.First(&after, task.Id).Error; err != nil || after.Name != task.Name || after.TargetFamily != "ipv6" {
		t.Fatalf("draft task changed: %+v err=%v", after, err)
	}
	for _, handler := range []func(*rpc.JsonRpcRequest) *rpc.JsonRpcError{
		func(req *rpc.JsonRpcRequest) *rpc.JsonRpcError {
			_, err := adminPlanConfigImport(t.Context(), req)
			return err
		},
		func(req *rpc.JsonRpcRequest) *rpc.JsonRpcError {
			_, err := adminImportConfig(t.Context(), req)
			return err
		},
	} {
		for _, field := range []string{"target_family", "TARGET_FAMILY", "TargetFamily", "pinned_generation", "PinnedGeneration", "`pinned_generation`"} {
			req := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Params: map[string]any{"document": map[string]any{
				"schema_version": 1, "ping_tasks": []map[string]any{{"id": task.Id, field: "invalid"}},
			}}}
			if err := handler(req); err == nil || err.Code != rpc.InvalidParams {
				t.Fatalf("draft pinned import accepted %q: %+v", field, err)
			}
		}
	}
}

func TestDraftPinnedRPCReportsCommittedReloadFailure(t *testing.T) {
	request := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Params: map[string]any{
		"name": "test", "target": "example.test:80", "type": "tcp", "interval": 60, "clients": []string{"node"}, "target_family": "ipv6",
	}}
	_, addErr := adminAddPingTaskWith(t.Context(), request, func(_ context.Context, task *models.PingTask) error {
		task.Id = 27
		return &tasks.ScheduleReloadPendingError{TaskID: 27, Err: errors.New("private scheduler detail")}
	})
	if addErr == nil {
		t.Fatal("create hid the pending schedule")
	}
	data, ok := addErr.Data.(rpc.CommittedErrorData)
	if addErr.Code != rpc.Unavailable || !ok || data.TaskID != 27 || !data.Committed || data.ErrorKey != "ping_create_schedule_pending" || !strings.Contains(addErr.Message, "Do not create it again") || strings.Contains(addErr.Message, "private scheduler detail") {
		t.Fatalf("create response omitted committed status: %+v", addErr)
	}
	editRequest := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Params: map[string]any{"tasks": []map[string]any{{"id": 27}}}}
	_, editErr := adminEditPingTaskWith(editRequest, true, func([]tasks.PingTaskEdit) error {
		return &tasks.ScheduleReloadPendingError{Err: errors.New("private scheduler detail")}
	})
	if editErr == nil || editErr.Code != rpc.Unavailable || !strings.Contains(editErr.Message, "Changes were saved") || strings.Contains(editErr.Message, "private scheduler detail") {
		t.Fatalf("edit response omitted committed status: %+v", editErr)
	}
	deleteRequest := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Params: map[string]any{"id": []uint{27}}}
	_, deleteErr := adminDeletePingTaskWith(deleteRequest, func([]uint) error {
		return &tasks.PingDeletionPendingError{Err: errors.New("private metric-store detail")}
	})
	if deleteErr == nil {
		t.Fatal("delete hid pending cleanup")
	}
	deleteData, ok := deleteErr.Data.(rpc.CommittedErrorData)
	if deleteErr.Code != rpc.Unavailable || !ok || !deleteData.Committed || deleteData.ErrorKey != "ping_delete_cleanup_pending" || strings.Contains(deleteErr.Message, "private metric-store detail") {
		t.Fatalf("delete response omitted committed status: %+v", deleteErr)
	}
}

// Exercise the real REST bridge, authorization and RPC decoding with a
// separate injected scheduler as well as the production method.
func TestDraftPinnedRESTBridgeAndAuthorization(t *testing.T) {
	db := dbcore.GetDBInstance()
	for _, id := range []string{"one", "two", "three"} {
		node := models.Client{UUID: id, Token: "pinned-rest-fixture-" + id}
		if err := db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Delete(&node).Error })
	}
	const method = "admin:draftPinnedAddFixture"
	if err := rpc.Register(method, func(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		return adminAddPingTaskWith(ctx, req, func(ctx context.Context, task *models.PingTask) error {
			return tasks.AddPingTaskPinnedDraft(ctx, db, task, func() error { return nil })
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rpc.Unregister(method) })

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") == "yes" {
			api.SetPrincipal(c, rpc.NewUserPrincipal("fixture-admin"))
		}
	})
	engine.POST("/draft/ping/add", Bind(method))
	engine.POST("/api/admin/ping/add", Bind("admin:addPingTask"))
	body := `{"name":"REST IPv6 fixture","target":"tj-ct-v6.ip.zstaticcdn.com:80","type":"auto","interval":60,"clients":["one","two","three"],"target_family":"ipv6","default_on":false}`
	call := func(path string, admin bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if admin {
			req.Header.Set("X-Test-Admin", "yes")
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if out := call("/draft/ping/add", false); out.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous draft creation: %d %s", out.Code, out.Body)
	}
	out := call("/draft/ping/add", true)
	if out.Code != http.StatusOK {
		t.Fatalf("draft REST creation: %d %s", out.Code, out.Body)
	}
	var response struct {
		Status string `json:"status"`
		Data   struct {
			ID uint `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil || response.Status != "success" || response.Data.ID == 0 {
		t.Fatalf("draft response: %s err=%v", out.Body, err)
	}
	t.Cleanup(func() { _ = db.Delete(&models.PingTask{}, response.Data.ID).Error })
	production := call("/api/admin/ping/add", true)
	if production.Code != http.StatusOK {
		t.Fatalf("production REST creation: %d %s", production.Code, production.Body)
	}
	var productionResponse struct {
		Data struct {
			ID uint `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(production.Body.Bytes(), &productionResponse); err != nil || productionResponse.Data.ID == 0 {
		t.Fatalf("production response: %s err=%v", production.Body, err)
	}
	t.Cleanup(func() {
		_ = db.Delete(&models.PingTask{}, productionResponse.Data.ID).Error
		_ = tasks.ReloadPingSchedule()
	})
	var stored models.PingTask
	if err := db.Take(&stored, response.Data.ID).Error; err != nil || stored.TargetFamily != "ipv6" ||
		stored.Target != "tj-ct-v6.ip.zstaticcdn.com:80" || stored.Type != "auto" || stored.Interval != 60 ||
		stored.DefaultOn || len(stored.Clients) != 3 || len(stored.PinnedGeneration) != 32 {
		t.Fatalf("draft REST stored=%+v err=%v", stored, err)
	}
}

func TestPinnedDiagnosticRESTRequiresAdminAndCurrentAssignment(t *testing.T) {
	db := dbcore.GetDBInstance()
	node := models.Client{UUID: "pinned-diagnostic-rest-node", Token: "fixture-token"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&node).Error })
	task := models.PingTask{Name: "diagnostic fixture", Target: "example.test:80", Type: "auto", Interval: 60,
		TargetFamily: "ipv6", PinnedGeneration: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Clients: models.StringArray{node.UUID}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") == "yes" {
			api.SetPrincipal(c, rpc.NewUserPrincipal("fixture-admin"))
		} else if c.GetHeader("X-Test-Key") == "yes" {
			api.SetPrincipal(c, rpc.NewScopedAPIKeyPrincipal("fixture-key", rpc.APIKeyScopes()))
		}
	})
	engine.POST("/diagnostic", Bind("admin:getPinnedPingDiagnostic"))
	call := func(body any, admin bool) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/diagnostic", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if admin {
			req.Header.Set("X-Test-Admin", "yes")
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	params := map[string]any{"task_id": task.Id, "client": node.UUID}
	if out := call(params, false); out.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous diagnostic: %d %s", out.Code, out.Body)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	keyReq := httptest.NewRequest(http.MethodPost, "/diagnostic", bytes.NewReader(data))
	keyReq.Header.Set("X-Test-Key", "yes")
	keyResp := httptest.NewRecorder()
	engine.ServeHTTP(keyResp, keyReq)
	if keyResp.Code != http.StatusUnauthorized || rpc.APIKeyHTTPAllowed(rpc.NewScopedAPIKeyPrincipal("fixture-key", rpc.APIKeyScopes()), http.MethodPost, "/api/admin/ping/diagnostic") {
		t.Fatalf("API key reached diagnostic: %d %s", keyResp.Code, keyResp.Body)
	}
	if out := call(map[string]any{"task_id": "bad", "client": node.UUID}, true); out.Code != http.StatusBadRequest {
		t.Fatalf("malformed task ID: %d %s", out.Code, out.Body)
	}
	if out := call(map[string]any{"task_id": task.Id, "client": "unassigned"}, true); out.Code != http.StatusNotFound {
		t.Fatalf("unassigned diagnostic: %d %s", out.Code, out.Body)
	}
	out := call(params, true)
	if out.Code != http.StatusOK {
		t.Fatalf("admin diagnostic: %d %s", out.Code, out.Body)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			State string `json:"state"`
			Legs  []struct {
				Status string `json:"status"`
			} `json:"legs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil || body.Status != "success" ||
		body.Data.State != "offline" || len(body.Data.Legs) != 1 || body.Data.Legs[0].Status != "unknown" {
		t.Fatalf("unexpected diagnostic: %s err=%v", out.Body, err)
	}
}

func TestPinnedDiagnosticRejectsBusyRequests(t *testing.T) {
	for i := 0; i < cap(pinnedDiagnosticSlots); i++ {
		pinnedDiagnosticSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(pinnedDiagnosticSlots); i++ {
			<-pinnedDiagnosticSlots
		}
	}()
	_, err := adminGetPinnedPingDiagnostic(t.Context(), &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION,
		Params: map[string]any{"task_id": 1, "client": "node"}})
	if err == nil || err.Code != rpc.Unavailable {
		t.Fatalf("over-capacity request = %+v", err)
	}
}

func TestDraftPinnedRPCAddPersistsExplicitFamily(t *testing.T) {
	db := dbcore.GetDBInstance()
	for _, id := range []string{"rpc-node-1", "rpc-node-2", "rpc-node-3"} {
		node := models.Client{UUID: id, Token: "pinned-rpc-fixture-" + id}
		if err := db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Delete(&node).Error })
	}
	reloads := 0
	add := func(ctx context.Context, task *models.PingTask) error {
		return tasks.AddPingTaskPinnedDraft(ctx, db, task, func() error { reloads++; return nil })
	}
	base := map[string]any{"name": "draft-rpc-create", "target": "tj-ct-v6.ip.zstaticcdn.com:80", "type": "auto", "interval": 60,
		"clients": []string{"rpc-node-1", "rpc-node-2", "rpc-node-3"}, "target_family": "ipv6", "default_on": false}
	call := func(params map[string]any) (any, *rpc.JsonRpcError) {
		return adminAddPingTaskWith(t.Context(), &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: "admin:addPingTask", Params: params}, add)
	}
	result, rpcErr := call(base)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	id, ok := result.(map[string]any)["task_id"].(uint)
	if !ok || id == 0 {
		t.Fatalf("invalid task ID: %#v", result)
	}
	t.Cleanup(func() { _ = db.Delete(&models.PingTask{}, id).Error })
	var saved models.PingTask
	if err := db.Take(&saved, id).Error; err != nil || saved.Target != base["target"] || saved.TargetFamily != "ipv6" || len(saved.Clients) != 3 || saved.DefaultOn || len(saved.PinnedGeneration) != 32 || saved.Weight != int(id) || reloads != 1 {
		t.Fatalf("stored=%+v reloads=%d err=%v", saved, reloads, err)
	}
	for _, tc := range []struct {
		field string
		value any
	}{
		{"target_family", "IPv6"}, {"target_family", "ipv4"}, {"interval", -1},
		{"target", "[2001:db8::1]:0"}, {"type", "invalid"}, {"reference", map[string]any{"bad": true}},
	} {
		params := make(map[string]any, len(base))
		for key, value := range base {
			params[key] = value
		}
		params[tc.field] = tc.value
		// An IPv4 pin is valid for a hostname; use an IPv6 literal to test a
		// contradictory family without relying on panel-side DNS.
		if tc.field == "target_family" && tc.value == "ipv4" {
			params["target"] = "[2001:db8::1]:80"
		}
		if _, err := call(params); err == nil || err.Code != rpc.InvalidParams {
			t.Fatalf("invalid %s=%v returned %+v", tc.field, tc.value, err)
		}
	}
	if reloads != 1 {
		t.Fatalf("invalid calls reloaded %d times", reloads)
	}
}

func TestDraftPinnedRPCEditPreservesOmittedFieldsAndBatchAtomicity(t *testing.T) {
	db := dbcore.GetDBInstance()
	const prefix = "draft-pinned-rpc-"
	for _, node := range []models.Client{
		{UUID: prefix + "dual", Token: prefix + "dual-token", IPv4: "192.0.2.1", IPv6: "2001:db8::1"},
		{UUID: prefix + "v4", Token: prefix + "v4-token", IPv4: "192.0.2.2"},
	} {
		if err := db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Delete(&node).Error })
	}
	pinned := models.PingTask{Name: "pinned-rpc", Type: "auto", Target: "example.test:80", TargetFamily: "ipv6", PinnedGeneration: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Interval: 60, Clients: models.StringArray{prefix + "dual", prefix + "v4"}, Reference: "gateway"}
	legacy := models.PingTask{Name: "legacy-rpc", Type: "tcp", Target: "example.test:80", Interval: 60, Clients: models.StringArray{prefix + "v4"}}
	for _, task := range []*models.PingTask{&pinned, &legacy} {
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Delete(task).Error })
	}
	reloads := 0
	apply := func(edits []tasks.PingTaskEdit) error {
		return tasks.EditPingTasksPinnedDraft(t.Context(), db, edits, func() error { reloads++; return nil })
	}
	call := func(rows ...map[string]any) *rpc.JsonRpcError {
		t.Helper()
		_, err := adminEditPingTaskWith(&rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: "admin:editPingTask", Params: map[string]any{"tasks": rows}}, true, apply)
		return err
	}
	row := func(task models.PingTask) map[string]any {
		return map[string]any{"id": task.Id, "name": task.Name, "type": task.Type, "target": task.Target, "interval": task.Interval, "clients": task.Clients}
	}
	pinnedRow := row(pinned)
	pinnedRow["name"] = "renamed"
	legacyRow := row(legacy)
	legacyRow["clients"] = []string{prefix + "dual", prefix + "v4"}
	if err := call(pinnedRow, legacyRow); err == nil || err.Code != rpc.InvalidParams {
		t.Fatalf("unsafe mixed batch returned %+v", err)
	}
	var got models.PingTask
	if err := db.Take(&got, pinned.Id).Error; err != nil || got.Name != pinned.Name || got.PinnedGeneration != pinned.PinnedGeneration || reloads != 0 {
		t.Fatalf("invalid batch changed pinned row: %+v reloads=%d err=%v", got, reloads, err)
	}
	legacyRow["target"] = "192.0.2.4:80"
	if err := call(pinnedRow, legacyRow); err != nil {
		t.Fatalf("mixed valid edit: %+v", err)
	}
	if err := db.Take(&got, pinned.Id).Error; err != nil || got.Name != "renamed" || got.Reference != pinned.Reference || got.TargetFamily != pinned.TargetFamily || got.PinnedGeneration == pinned.PinnedGeneration || reloads != 1 {
		t.Fatalf("omitted fields were not preserved: %+v reloads=%d err=%v", got, reloads, err)
	}
	for _, change := range []struct{ field, value string }{{"target_family", ""}, {"target_family", "ipv4"}, {"target", "other.test:80"}, {"reference", ""}, {"type", "tcp"}} {
		bad := row(got)
		bad[change.field] = change.value
		if err := call(bad); err == nil {
			t.Fatalf("changing %s to %q changed measurement identity", change.field, change.value)
		}
	}
	bad := row(got)
	bad["reference"] = map[string]any{"unexpected": true}
	if err := call(bad); err == nil || err.Code != rpc.InvalidParams {
		t.Fatalf("malformed reference: %+v", err)
	}
	if err := tasks.EditPingTasksPinnedDraft(context.Background(), db, []tasks.PingTaskEdit{{Task: &models.PingTask{Id: got.Id + 1000000}}}, func() error { return nil }); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing ID = %v", err)
	}
}

func TestDraftPinnedConfigurationExportAndImportFailClosed(t *testing.T) {
	db := dbcore.GetDBInstance()
	task := models.PingTask{Name: "draft-export", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", PinnedGeneration: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Interval: 60}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	document, err := configxport.Export(exportStore{}, false, "test")
	if err != nil || document.SchemaVersion != configxport.IdentitySchemaVersion {
		t.Fatalf("pinned export schema=%d err=%v", document.SchemaVersion, err)
	}
	var found bool
	for _, record := range document.PingTasks {
		if record["name"] == task.Name {
			found = true
			if record["target_family"] != "ipv6" || record["target"] != task.Target {
				t.Fatalf("pinned export lost its semantics: %+v", record)
			}
			if _, exists := record["pinned_generation"]; exists {
				t.Fatal("pinned export included a runtime generation")
			}
		}
	}
	if !found {
		t.Fatal("pinned export omitted the task")
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := configxport.Parse(encoded); err != nil || parsed.SchemaVersion != configxport.IdentitySchemaVersion {
		t.Fatalf("pinned export parse failed: schema=%d err=%v", parsed.SchemaVersion, err)
	}
	family, generation := task.TargetFamily, task.PinnedGeneration
	for _, malformed := range []struct{ family, generation string }{
		{"ipv6", ""}, {"invalid", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	} {
		if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Updates(map[string]any{"target_family": malformed.family, "pinned_generation": malformed.generation}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := configxport.Export(exportStore{}, false, "test"); err == nil {
			t.Fatalf("export accepted invalid family/generation: %+v", malformed)
		}
	}
	if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Updates(map[string]any{"target_family": family, "pinned_generation": generation}).Error; err != nil {
		t.Fatal(err)
	}
	// An unrelated change must not be applied before the pinned-task check,
	// even when the incoming document has no target_family field at all.
	client := models.Client{UUID: "draft-pin-import-node", Token: "draft-pin-import-token", Name: "before"}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&client).Error })
	req := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Params: map[string]any{"document": map[string]any{
		"schema_version": 1, "clients": []map[string]any{{"uuid": client.UUID, "name": "after"}},
		"ping_tasks": []map[string]any{{"id": task.Id, "name": "unpin-by-omission"}},
	}}}
	for _, handler := range []func() *rpc.JsonRpcError{
		func() *rpc.JsonRpcError { _, err := adminPlanConfigImport(t.Context(), req); return err },
		func() *rpc.JsonRpcError { _, err := adminImportConfig(t.Context(), req); return err },
	} {
		if err := handler(); err == nil || err.Code != rpc.InvalidParams {
			t.Fatalf("import did not fail before writes: %+v", err)
		}
	}
	var saved models.Client
	if err := db.Where("uuid = ?", client.UUID).Take(&saved).Error; err != nil || saved.Name != "before" {
		t.Fatalf("client was changed before the refusal: %+v err=%v", saved, err)
	}
	var savedTask models.PingTask
	if err := db.Take(&savedTask, task.Id).Error; err != nil || savedTask.TargetFamily != "ipv6" || savedTask.Name != task.Name {
		t.Fatalf("task changed during refusal: %+v err=%v", savedTask, err)
	}
}

func TestDraftPinnedImportFenceCoversCheckAndApply(t *testing.T) {
	db := dbcore.GetDBInstance()
	client := models.Client{UUID: "draft-import-gap-node", Token: "draft-import-gap-token", Name: "before"}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&client).Error })
	docBytes, err := json.Marshal(rpc.NewRequest(1, "admin:planConfigImport", map[string]any{"document": map[string]any{
		"schema_version": 1, "clients": []map[string]any{{"uuid": client.UUID, "name": "after"}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	doc, parseErr := rpc.ParseRequest(docBytes)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	task := models.PingTask{Name: "concurrent-pin", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", PinnedGeneration: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Interval: 60, Clients: models.StringArray{client.UUID}}
	task.PinnedGeneration = ""
	if err := pingguard.WithConfigImport(t.Context(), func() error {
		if _, err := bindDocument(doc); err != nil {
			t.Fatalf("preflight failed: %+v", err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		if err := tasks.AddPingTaskPinnedDraft(ctx, db, &task, func() error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("concurrent creation crossed import fence: %v", err)
		}
		return applyRecord("clients", client.UUID, map[string]any{"name": "after"}, false)
	}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.AddPingTaskPinnedDraft(t.Context(), db, &task, func() error { return nil }); err != nil {
		t.Fatalf("create after import released fence: %v", err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	if _, err := adminImportConfig(t.Context(), doc); err == nil || err.Code != rpc.InvalidParams {
		t.Fatalf("import without a preview should fail before writes: %+v", err)
	}
	var got models.Client
	if err := db.Take(&got, "uuid = ?", client.UUID).Error; err != nil || got.Name != "after" {
		t.Fatalf("unexpected client state: %+v err=%v", got, err)
	}
}
