package migration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	appconfig "github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/migrations"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupConfigDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "migration.db"))+"?mode=rwc"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get migration sql database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	appconfig.SetDb(db)
	return db
}

func TestQuiesceWaitsForWorkerCleanupAndRejectsNewWork(t *testing.T) {
	c := NewStructureController()
	c.Activate()
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c.mu.Lock()
	c.status.State = "copying"
	c.launchLocked(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(canceled)
		// A failed status must not admit a second worker while deferred cleanup
		// in the first worker is still running.
		c.fail("canceled", "failed")
		<-release
	})
	c.mu.Unlock()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- c.Quiesce(ctx) }()
	<-canceled

	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, APIPath+"/start", nil)
	c.startStructure(ginContext)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("new work during shutdown: status %d, want 409", recorder.Code)
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("quiesce while cleanup is blocked = %v, want deadline", err)
	}
	close(release)
	if err := c.Quiesce(context.Background()); err != nil {
		t.Fatalf("retry quiesce: %v", err)
	}
	recorder = httptest.NewRecorder()
	ginContext, _ = gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, APIPath+"/start", nil)
	c.startStructure(ginContext)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("new work after shutdown: status %d, want 409", recorder.Code)
	}
}

func TestCompletedStatusCannotRaceWorkerCleanup(t *testing.T) {
	c := NewStructureController()
	release := make(chan struct{})
	c.mu.Lock()
	c.status.State = "failed"
	c.launchLocked(func(context.Context) { <-release })
	if !c.operationActiveLocked() {
		t.Fatal("worker cleanup was not considered active")
	}
	c.mu.Unlock()
	close(release)
	if err := c.Quiesce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMetricConfigValidatesSelectedDriverAgainstDSN(t *testing.T) {
	setupConfigDB(t)

	sqliteConfig, err := metricConfig("sqlite", "")
	if err != nil {
		t.Fatalf("build default SQLite config: %v", err)
	}
	if sqliteConfig.Driver != "sqlite" || sqliteConfig.DSN != "./data/metrics.db" {
		t.Fatalf("unexpected default SQLite config: %#v", sqliteConfig)
	}
	if _, err := metricConfig("mysql", "./data/metrics.db"); err == nil {
		t.Fatal("expected mismatched MySQL/SQLite DSN to fail")
	}
	postgresConfig, err := metricConfig("postgresql", "host=127.0.0.1 port=5432 user=komari password=secret dbname=komari sslmode=disable")
	if err != nil {
		t.Fatalf("build PostgreSQL config: %v", err)
	}
	if postgresConfig.Driver != "postgresql" {
		t.Fatalf("unexpected PostgreSQL driver: %q", postgresConfig.Driver)
	}
}

func TestRestrictedControllersRegisterUnifiedRoutes(t *testing.T) {
	for _, test := range []struct {
		name       string
		controller *Controller
		discard    bool
	}{
		{name: "legacy", controller: NewLegacyController(setupConfigDB(t), migrations.LegacyMonitoringSummary{})},
		{name: "structure", controller: NewStructureController(), discard: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(api.IdentityMiddleware())
			test.controller.Activate()
			test.controller.Register(r)

			routes := make(map[string]bool)
			for _, route := range r.Routes() {
				routes[route.Method+" "+route.Path] = true
			}
			base := APIPath
			for _, route := range []string{"GET " + base + "/auth", "GET " + base + "/status", "POST " + base + "/start"} {
				if !routes[route] {
					t.Fatalf("required restricted route is missing: %s", route)
				}
			}
			if routes["POST "+base+"/cleanup"] {
				t.Fatalf("full-history migration unexpectedly exposes cleanup: %s", base)
			}
			if routes["POST "+base+"/discard"] != test.discard {
				t.Fatalf("POST %s/discard presence = %v, want %v", base, routes["POST "+base+"/discard"], test.discard)
			}
			if routes["GET /api/public"] || routes["GET /api/rpc2"] || routes["GET /api/clients/v2/rpc"] {
				t.Fatalf("ordinary APIs leaked into restricted routes: %#v", routes)
			}

			request := httptest.NewRequest(http.MethodGet, APIPath+"/status", nil)
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated status code = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestUnifiedAuthIncludesMode(t *testing.T) {
	db := setupConfigDB(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.IdentityMiddleware())
	controller := NewLegacyController(db, migrations.LegacyMonitoringSummary{})
	controller.Activate()
	controller.Register(r)

	request := httptest.NewRequest(http.MethodGet, APIPath+"/auth", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", APIPath+"/auth", response.Code)
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode GET %s response: %v", APIPath+"/auth", err)
	}
	if got := payload.Data["mode"]; got != string(ModeLegacyMonitoring) {
		t.Fatalf("GET %s mode = %v, want %q", APIPath+"/auth", got, ModeLegacyMonitoring)
	}
}

func TestStructureProgressPercent(t *testing.T) {
	if got := structureProgressPercent(metricstore.RestructureProgress{RowsDone: 50, RowsTotal: 100}); got != 40 {
		t.Fatalf("copying percent = %v, want 40", got)
	}
	if got := structureProgressPercent(metricstore.RestructureProgress{Phase: "reclaiming"}); got != 80 {
		t.Fatalf("reclaiming percent = %v, want 80", got)
	}
	if got := structureProgressPercent(metricstore.RestructureProgress{RowsDone: 100, RowsTotal: 100}); got != 80 {
		t.Fatalf("capped percent = %v, want 80", got)
	}
}
