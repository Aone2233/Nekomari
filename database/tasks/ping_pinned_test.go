package tasks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func pinnedTaskDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.PingTask{}, &models.PingTaskDeletion{}, &models.Client{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := db.Create(&models.Client{UUID: id, Token: "fixture-token-" + id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestLegacyAndPinnedCreationWaitForConfigImport(t *testing.T) {
	db := pinnedTaskDB(t)
	if err := pingguard.WithConfigImport(t.Context(), func() error {
		for _, mode := range []string{"legacy", "pinned"} {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			var err error
			if mode == "legacy" {
				_, err = addPingTaskWith(ctx, db, []string{"a"}, false, "legacy", "127.0.0.1:80", "tcp", 60, "", func() error { return nil })
			} else {
				task := &models.PingTask{Name: "pinned", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"a"}}
				err = AddPingTaskPinnedDraft(ctx, db, task, func() error { return nil })
			}
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s creation crossed import fence: %v", mode, err)
			}
		}
		var count int64
		if err := db.Model(&models.PingTask{}).Count(&count).Error; err != nil || count != 0 {
			t.Errorf("fenced create wrote tasks: count=%d err=%v", count, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := addPingTaskWith(t.Context(), db, []string{"a"}, false, "legacy", "127.0.0.1:80", "tcp", 60, "", func() error { return nil }); err != nil {
		t.Fatalf("creation did not resume after import: %v", err)
	}
}

func TestLegacyCreateValidatesCurrentFamilyInsideCreationFence(t *testing.T) {
	db := pinnedTaskDB(t)
	if err := db.Model(&models.Client{}).Where("uuid IN ?", []string{"a", "b"}).Updates(map[string]any{"ipv4": "192.0.2.1", "ipv6": ""}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	result := make(chan error, 1)
	if err := pingguard.WithDefaultRoster(t.Context(), func() error {
		go func() {
			close(started)
			_, err := addPingTaskWith(ctx, db, []string{"a", "b"}, false, "legacy", "example.test:80", "tcp", 60, "", func() error { return nil })
			result <- err
		}()
		<-started
		// A pre-fence snapshot would see two IPv4-only nodes; the transaction
		// must instead validate after the preceding roster operation commits.
		return db.Model(&models.Client{}).Where("uuid = ?", "b").Updates(map[string]any{"ipv4": "", "ipv6": "2001:db8::1"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		var validation *PingTaskValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("mixed family creation accepted: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("legacy creation remained blocked")
	}
	var count int64
	if err := db.Model(&models.PingTask{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected creation wrote a task: count=%d err=%v", count, err)
	}
	if _, err := addPingTaskWith(t.Context(), db, []string{"a", "b"}, false, "literal", "192.0.2.1:80", "tcp", 60, "", func() error { return nil }); err != nil {
		t.Fatalf("literal target unexpectedly rejected: %v", err)
	}
}

func TestDraftMixedBatchValidatesPersistedModeAtomically(t *testing.T) {
	db := pinnedTaskDB(t)
	for _, node := range []models.Client{
		{UUID: "dual", Token: "dual-token", IPv4: "192.0.2.1", IPv6: "2001:db8::1"},
		{UUID: "v4", Token: "v4-token", IPv4: "192.0.2.2"},
	} {
		if err := db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
	}
	pinned := models.PingTask{Name: "pinned", Type: "auto", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"dual", "v4"}, Reference: "gateway"}
	if err := addPinnedPingTask(t.Context(), db, &pinned); err != nil {
		t.Fatal(err)
	}
	legacy := models.PingTask{Name: "legacy", Type: "tcp", Target: "example.test:80", Interval: 60, Clients: models.StringArray{"v4"}}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	pinnedEdit := pinned
	// Models decoded from older callers have no family or generation fields.
	pinnedEdit.TargetFamily, pinnedEdit.PinnedGeneration = "", ""
	pinnedEdit.Name = "pinned-updated"
	legacyEdit := legacy
	legacyEdit.Clients = models.StringArray{"v4", "dual"}
	reloads := 0
	reload := func() error { reloads++; return nil }
	err := editPingTasksWith(t.Context(), db, []PingTaskEdit{{Task: &pinnedEdit}, {Task: &legacyEdit}}, reload, true)
	var validation *PingTaskValidationError
	if !errors.As(err, &validation) || reloads != 0 {
		t.Fatalf("unsafe legacy edit: err=%v reloads=%d", err, reloads)
	}
	var saved models.PingTask
	if err := db.Take(&saved, pinned.Id).Error; err != nil || saved.Name != pinned.Name || saved.PinnedGeneration != pinned.PinnedGeneration {
		t.Fatalf("invalid mixed batch partially committed: %+v err=%v", saved, err)
	}
	legacyEdit.Target = "192.0.2.42:80"
	if err := editPingTasksWith(t.Context(), db, []PingTaskEdit{{Task: &pinnedEdit}, {Task: &legacyEdit}}, reload, true); err != nil {
		t.Fatal(err)
	}
	if err := db.Take(&saved, pinned.Id).Error; err != nil || saved.Name != pinnedEdit.Name || saved.TargetFamily != "ipv6" || saved.Reference != "gateway" || saved.PinnedGeneration == pinned.PinnedGeneration {
		t.Fatalf("old caller did not preserve pinned fields: %+v err=%v", saved, err)
	}
	if reloads != 1 {
		t.Fatalf("reloads=%d, want one successful batch", reloads)
	}
}

func TestDraftPinnedCreatePersistsGenerationAndRejectsInvalid(t *testing.T) {
	db := pinnedTaskDB(t)
	task := models.PingTask{Name: "ipv6", Type: "auto", Target: "tj-ct-v6.ip.zstaticcdn.com:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"a", "b", "c"}, Reference: "  gateway  "}
	if err := addPinnedPingTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	var stored models.PingTask
	if err := db.Take(&stored, task.Id).Error; err != nil {
		t.Fatal(err)
	}
	if task.Id == 0 || stored.PinnedGeneration != task.PinnedGeneration || len(stored.PinnedGeneration) != 32 || stored.Weight != int(task.Id) || stored.Target != task.Target || stored.Reference != "gateway" {
		t.Fatalf("stored task = %+v", stored)
	}
	if strconv.IntSize == 64 {
		overflow := int64(math.MaxInt64/int64(time.Second)) + 1
		invalid := models.PingTask{Name: "overflow", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: int(overflow), Clients: models.StringArray{"a"}}
		if err := addPinnedPingTask(t.Context(), db, &invalid); err == nil || invalid.Id != 0 {
			t.Fatalf("overflow create = %+v, %v", invalid, err)
		}
		edit := task
		edit.Interval = int(overflow)
		if err := pingguard.With(t.Context(), task.Id, func() error {
			return editPinnedPingTask(t.Context(), db, PingTaskEdit{Task: &edit}, nil)
		}); err == nil {
			t.Fatal("overflow edit accepted")
		}
		if err := db.Take(&stored, task.Id).Error; err != nil || stored.Interval != task.Interval || stored.PinnedGeneration != task.PinnedGeneration {
			t.Fatalf("overflow edit changed row: %+v, %v", stored, err)
		}
	}
	for _, mode := range []string{"missing-nodes", "bad-literal", "bad-family", "bad-protocol", "bad-interval", "long-name", "long-reference", "canceled"} {
		invalid := models.PingTask{Name: "invalid", Type: "auto", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"a"}}
		ctx := t.Context()
		switch mode {
		case "missing-nodes":
			invalid.Clients = nil
		case "bad-literal":
			invalid.Target = "127.0.0.1:80"
		case "bad-family":
			invalid.TargetFamily = "IPv6"
		case "bad-protocol":
			invalid.Type = "invalid"
		case "bad-interval":
			invalid.Interval = -1
		case "long-name":
			invalid.Name = strings.Repeat("a", 256)
		case "long-reference":
			invalid.Reference = strings.Repeat("a", 256)
		case "canceled":
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		if err := addPinnedPingTask(ctx, db, &invalid); err == nil || invalid.Id != 0 || invalid.PinnedGeneration != "" {
			t.Fatalf("%s created %+v err=%v", mode, invalid, err)
		}
	}
	var count int64
	if err := db.Model(&models.PingTask{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("invalid creates changed DB: count=%d err=%v", count, err)
	}
	future := models.PingTask{Name: "future nodes", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, DefaultOn: true}
	if err := addPinnedPingTask(t.Context(), db, &future); err != nil {
		t.Fatalf("default-on task with no current assignments: %v", err)
	}
}

func TestDraftPinnedMutationReportsCommittedReloadFailure(t *testing.T) {
	db := pinnedTaskDB(t)
	cause := errors.New("injected scheduler failure")
	failReload := func() error { return cause }
	task := models.PingTask{Name: "saved", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"a"}}
	err := AddPingTaskPinnedDraft(t.Context(), db, &task, failReload)
	var pending *ScheduleReloadPendingError
	if !errors.As(err, &pending) || !errors.Is(err, cause) || task.Id == 0 || pending.TaskID != task.Id {
		t.Fatalf("create did not disclose committed task: %+v, %v", task, err)
	}
	var saved models.PingTask
	if err := db.Take(&saved, task.Id).Error; err != nil || saved.Name != task.Name {
		t.Fatalf("create was not committed: %+v, %v", saved, err)
	}
	saved.Name = "edited"
	err = EditPingTasksPinnedDraft(t.Context(), db, []PingTaskEdit{{Task: &saved}}, failReload)
	pending = nil
	if !errors.As(err, &pending) || !errors.Is(err, cause) {
		t.Fatalf("edit did not disclose committed changes: %v", err)
	}
	var edited models.PingTask
	if err := db.Take(&edited, task.Id).Error; err != nil || edited.Name != "edited" || edited.PinnedGeneration == task.PinnedGeneration {
		t.Fatalf("edit was not committed: %+v, %v", edited, err)
	}
	if err := EditPingTasksPinnedDraft(t.Context(), db, []PingTaskEdit{{Task: &edited}}, func() error { return nil }); err != nil {
		t.Fatalf("a subsequent edit did not recover: %v", err)
	}
}

func TestDraftPinnedMutationRejectsDuplicateAssignments(t *testing.T) {
	db := pinnedTaskDB(t)
	for _, clients := range []models.StringArray{{"a", "a"}, {""}, {" a"}, {strings.Repeat("a", 65)}, make(models.StringArray, 4097)} {
		task := models.PingTask{Name: "invalid", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: clients}
		if err := addPinnedPingTask(t.Context(), db, &task); err == nil || task.Id != 0 {
			t.Fatalf("invalid assignments accepted: %v, err=%v", clients[:min(len(clients), 2)], err)
		}
	}
	task := models.PingTask{Name: "valid", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"a", "b"}}
	if err := addPinnedPingTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	edit := task
	edit.Clients = models.StringArray{"a", "a"}
	if err := pingguard.With(t.Context(), task.Id, func() error {
		return editPinnedPingTask(t.Context(), db, PingTaskEdit{Task: &edit}, nil)
	}); err == nil {
		t.Fatal("edit accepted duplicate assignments")
	}
	var stored models.PingTask
	if err := db.Take(&stored, task.Id).Error; err != nil || len(stored.Clients) != 2 || stored.Clients[1] != "b" || stored.PinnedGeneration != task.PinnedGeneration {
		t.Fatalf("invalid edit changed task: %+v err=%v", stored, err)
	}
}

func TestDraftPinnedMutationOrdersNodeDeletionAndValidatesExistence(t *testing.T) {
	db := pinnedTaskDB(t)
	newTask := func() models.PingTask {
		return models.PingTask{Name: "guarded", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"a"}}
	}
	missing := newTask()
	missing.Clients = models.StringArray{"absent"}
	if err := AddPingTaskPinnedDraft(t.Context(), db, &missing, func() error { t.Error("missing node reloaded schedule"); return nil }); err == nil || missing.Id != 0 {
		t.Fatalf("missing node was accepted: %+v err=%v", missing, err)
	}
	task := newTask()
	if err := clientguard.With(t.Context(), "a", func() error {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		return AddPingTaskPinnedDraft(ctx, db, &task, func() error { return nil })
	}); !errors.Is(err, context.DeadlineExceeded) || task.Id != 0 {
		t.Fatalf("creation crossed node guard: task=%+v err=%v", task, err)
	}
	if err := AddPingTaskPinnedDraft(t.Context(), db, &task, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	edit := task
	edit.Name = "changed"
	edit.Clients = models.StringArray{"absent"}
	if err := EditPingTasksPinnedDraft(t.Context(), db, []PingTaskEdit{{Task: &edit}}, func() error { t.Error("missing node reloaded schedule"); return nil }); err == nil {
		t.Fatal("edit accepted a missing node")
	}
	var saved models.PingTask
	if err := db.Take(&saved, task.Id).Error; err != nil || saved.Name != task.Name || saved.PinnedGeneration != task.PinnedGeneration {
		t.Fatalf("invalid edit changed task: %+v err=%v", saved, err)
	}
	edit.Clients = models.StringArray{"b"}
	if err := clientguard.With(t.Context(), "b", func() error {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		return EditPingTasksPinnedDraft(ctx, db, []PingTaskEdit{{Task: &edit}}, func() error { return nil })
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("edit crossed node guard: %v", err)
	}
}

func TestDraftPinnedMutationsWaitForDefaultRoster(t *testing.T) {
	db := pinnedTaskDB(t)
	task := models.PingTask{Name: "roster", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, DefaultOn: true, Clients: models.StringArray{"a"}}
	if err := addPinnedPingTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	err := pingguard.WithDefaultRoster(t.Context(), func() error {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		fresh := models.PingTask{Name: "new", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, DefaultOn: true}
		if err := AddPingTaskPinnedDraft(ctx, db, &fresh, func() error { t.Error("blocked create reloaded"); return nil }); !errors.Is(err, context.DeadlineExceeded) || fresh.Id != 0 {
			t.Fatalf("create crossed roster fence: id=%d err=%v", fresh.Id, err)
		}
		ctxEdit, cancelEdit := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancelEdit()
		edit := task
		edit.Name = "changed"
		if err := EditPingTasksPinnedDraft(ctxEdit, db, []PingTaskEdit{{Task: &edit}}, func() error { t.Error("blocked edit reloaded"); return nil }); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("edit crossed roster fence: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var saved models.PingTask
	if err := db.Take(&saved, task.Id).Error; err != nil || saved.Name != task.Name || saved.PinnedGeneration != task.PinnedGeneration {
		t.Fatalf("blocked edits changed task: %+v err=%v", saved, err)
	}
}

func TestClientCreationAndDefaultAssignmentsAreAtomic(t *testing.T) {
	db := pinnedTaskDB(t)
	pinned := models.PingTask{Name: "pinned", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, DefaultOn: true, Clients: models.StringArray{"a"}}
	if err := AddPingTaskPinnedDraft(t.Context(), db, &pinned, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	legacy := models.PingTask{Name: "legacy", Type: "icmp", Target: "192.0.2.1", Interval: 60, DefaultOn: true, Clients: models.StringArray{"a"}}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	create := func(node *models.Client) (bool, error) {
		var changed bool
		err := pingguard.WithDefaultRoster(t.Context(), func() error {
			return clientguard.With(t.Context(), node.UUID, func() error {
				var err error
				changed, err = CreateClientWithDefaultPing(t.Context(), db, node)
				return err
			})
		})
		return changed, err
	}
	node := models.Client{UUID: "future", Token: "future-token"}
	if changed, err := create(&node); err != nil || !changed {
		t.Fatalf("client create changed=%v err=%v", changed, err)
	}
	for _, original := range []models.PingTask{pinned, legacy} {
		var got models.PingTask
		if err := db.Take(&got, original.Id).Error; err != nil || !got.AppliesToClient(node.UUID) {
			t.Fatalf("task %d missing default assignment: %+v err=%v", original.Id, got, err)
		}
		if original.TargetFamily != "" && (!pingguard.ValidGeneration(got.PinnedGeneration) || got.PinnedGeneration == original.PinnedGeneration) {
			t.Fatalf("pinned generation not renewed: %+v", got)
		}
	}
	if err := db.Exec(fmt.Sprintf(`CREATE TRIGGER reject_default_assignment BEFORE UPDATE ON ping_tasks WHEN OLD.id = %d BEGIN SELECT RAISE(ABORT, 'assignment blocked'); END`, legacy.Id)).Error; err != nil {
		t.Fatal(err)
	}
	failed := models.Client{UUID: "rollback", Token: "rollback-token"}
	if changed, err := create(&failed); err == nil || changed {
		t.Fatalf("failed assignment committed client: changed=%v err=%v", changed, err)
	}
	var count int64
	if err := db.Model(&models.Client{}).Where("uuid = ?", failed.UUID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed client exists: count=%d err=%v", count, err)
	}
	var after models.PingTask
	if err := db.Take(&after, pinned.Id).Error; err != nil || after.AppliesToClient(failed.UUID) {
		t.Fatalf("failed client partially assigned: %+v err=%v", after, err)
	}
}

func TestPingScheduleReconciliationRetriesOnlyPendingReload(t *testing.T) {
	pingReloadMu.Lock()
	pingReloadPending = false
	pingReloadMu.Unlock()
	t.Cleanup(func() {
		pingReloadMu.Lock()
		pingReloadPending = false
		pingReloadMu.Unlock()
	})
	calls := 0
	cause := errors.New("temporary database failure")
	fail := func() error { calls++; return cause }
	recover := func() error { calls++; return nil }
	if attempted, err := reloadPingScheduleWith(true, fail); attempted || err != nil || calls != 0 {
		t.Fatalf("healthy scheduler was reloaded: attempted=%v err=%v calls=%d", attempted, err, calls)
	}
	if attempted, err := reloadPingScheduleWith(false, fail); !attempted || !errors.Is(err, cause) || calls != 1 {
		t.Fatalf("initial failure lost: attempted=%v err=%v calls=%d", attempted, err, calls)
	}
	if !PingScheduleReloadPending() {
		t.Fatal("failed reload is not visible")
	}
	if attempted, err := reloadPingScheduleWith(true, fail); !attempted || !errors.Is(err, cause) || calls != 2 {
		t.Fatalf("failure was not retried: attempted=%v err=%v calls=%d", attempted, err, calls)
	}
	if attempted, err := reloadPingScheduleWith(true, recover); !attempted || err != nil || calls != 3 {
		t.Fatalf("recovery failed: attempted=%v err=%v calls=%d", attempted, err, calls)
	}
	if PingScheduleReloadPending() {
		t.Fatal("successful reload did not clear visible pending state")
	}
	if attempted, err := reloadPingScheduleWith(true, fail); attempted || err != nil || calls != 3 {
		t.Fatalf("recovered scheduler was unnecessarily reloaded: attempted=%v err=%v calls=%d", attempted, err, calls)
	}
}

func TestDraftPinnedEditRevertAssignmentsAndHistoryBoundary(t *testing.T) {
	db := pinnedTaskDB(t)
	initial := models.PingTask{Name: "fixture", Type: "auto", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"a"}, Reference: "gateway"}
	if err := addPinnedPingTask(t.Context(), db, &initial); err != nil {
		t.Fatal(err)
	}
	current := initial
	seen := map[string]bool{initial.PinnedGeneration: true}
	apply := func(next models.PingTask, ref, family *string) {
		t.Helper()
		if err := pingguard.With(t.Context(), initial.Id, func() error {
			return editPinnedPingTask(t.Context(), db, PingTaskEdit{Task: &next, Reference: ref}, family)
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.Take(&current, initial.Id).Error; err != nil {
			t.Fatal(err)
		}
		if len(current.PinnedGeneration) != 32 || seen[current.PinnedGeneration] {
			t.Fatalf("generation reused: %+v", current)
		}
		seen[current.PinnedGeneration] = true
	}
	changed := initial
	changed.Name = "renamed"
	apply(changed, nil, nil)
	if current.Reference != "gateway" {
		t.Fatal("omitted reference was cleared")
	}
	apply(initial, nil, nil)
	changed = initial
	changed.Clients = models.StringArray{}
	apply(changed, nil, nil)
	apply(initial, nil, nil)
	for _, tc := range []struct {
		name string
		task models.PingTask
		ref  *string
	}{
		{name: "target", task: func() models.PingTask { x := initial; x.Target = "other.test:443"; return x }()},
		{name: "protocol", task: func() models.PingTask { x := initial; x.Type = "tcp"; return x }()},
		{name: "reference", task: initial, ref: new(string)},
	} {
		before := current.PinnedGeneration
		err := pingguard.With(t.Context(), initial.Id, func() error {
			return editPinnedPingTask(t.Context(), db, PingTaskEdit{Task: &tc.task, Reference: tc.ref}, nil)
		})
		if err == nil {
			t.Fatalf("accepted change of %s within the same history", tc.name)
		}
		if err := db.Take(&current, initial.Id).Error; err != nil || current.PinnedGeneration != before || current.Reference != "gateway" {
			t.Fatalf("refused %s changed row: %+v err=%v", tc.name, current, err)
		}
	}
	for _, family := range []string{"", "ipv4"} {
		before := current.PinnedGeneration
		err := pingguard.With(t.Context(), initial.Id, func() error {
			return editPinnedPingTask(t.Context(), db, PingTaskEdit{Task: &initial}, &family)
		})
		if err == nil {
			t.Fatalf("accepted family transition %q", family)
		}
		if err := db.Take(&current, initial.Id).Error; err != nil || current.PinnedGeneration != before || current.TargetFamily != "ipv6" {
			t.Fatalf("refused transition changed row: %+v err=%v", current, err)
		}
	}
	legacy := models.PingTask{Name: "legacy", Type: "tcp", Target: "127.0.0.1:80", Interval: 60}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	family := "ipv6"
	if err := editPinnedPingTask(t.Context(), db, PingTaskEdit{Task: &legacy}, &family); err == nil {
		t.Fatal("legacy history was silently converted to pinned")
	}
}

func TestDraftPinnedRecreateGetsNewIncarnation(t *testing.T) {
	db := pinnedTaskDB(t)
	task := models.PingTask{Name: "fixture", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"a"}}
	if err := addPinnedPingTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	first := task.PinnedGeneration
	if err := db.Delete(&task).Error; err != nil {
		t.Fatal(err)
	}
	task.Id, task.PinnedGeneration = 0, ""
	if err := addPinnedPingTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	if task.PinnedGeneration == first {
		t.Fatal("recreated task reused generation")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := editPinnedPingTask(ctx, db, PingTaskEdit{Task: &task}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled edit = %v", err)
	}
}

func TestPinnedClientUpdatesRenewGenerationAtomically(t *testing.T) {
	db := pinnedTaskDB(t)
	initial, err := pingguard.NewGeneration()
	if err != nil {
		t.Fatal(err)
	}
	task := models.PingTask{Name: "test", Type: "tcp", Target: "example.com:80", TargetFamily: "ipv6", PinnedGeneration: initial, Clients: models.StringArray{"a"}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	for _, clients := range []models.StringArray{{"b"}, {"a"}} {
		previous := task.PinnedGeneration
		if err := pingguard.With(t.Context(), task.Id, func() error { return updatePingTaskClients(db, task, clients) }); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&task, task.Id).Error; err != nil {
			t.Fatal(err)
		}
		if task.PinnedGeneration == "" || task.PinnedGeneration == previous || task.PinnedGeneration == initial || len(task.Clients) != 1 || task.Clients[0] != clients[0] {
			t.Fatalf("assignment did not advance incarnation: %+v", task)
		}
	}
}

func TestPinnedBatchMutationIsAtomicAndFencesReassignment(t *testing.T) {
	db := pinnedTaskDB(t)
	first := models.PingTask{Name: "first", Type: "tcp", Target: "example.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{"a"}, Reference: "gateway"}
	second := models.PingTask{Name: "second", Type: "auto", Target: "second.test:80", TargetFamily: "ipv4", Interval: 60, Clients: models.StringArray{"b"}}
	for _, item := range []*models.PingTask{&first, &second} {
		if err := addPinnedPingTask(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	reloads := 0
	reload := func() error { reloads++; return nil }
	changed := first
	changed.Name = "changed"
	invalid := second
	invalid.Target = "other.test:80"
	err := editPingTasksWith(t.Context(), db, []PingTaskEdit{{Task: &changed}, {Task: &invalid}}, reload, true)
	if err == nil || reloads != 0 {
		t.Fatalf("invalid batch: err=%v reloads=%d", err, reloads)
	}
	var got models.PingTask
	if err := db.Take(&got, first.Id).Error; err != nil || got.Name != first.Name || got.PinnedGeneration != first.PinnedGeneration {
		t.Fatalf("batch partially committed: %+v err=%v", got, err)
	}
	if err := editPingTasksWith(t.Context(), db, []PingTaskEdit{{Task: &changed}, {Task: &changed}}, reload, true); err == nil {
		t.Fatal("duplicate task IDs were accepted")
	}
	seen := map[string]bool{first.PinnedGeneration: true}
	for _, members := range []models.StringArray{{"b"}, {"a"}} {
		changed.Clients = members
		if err := editPingTasksWith(t.Context(), db, []PingTaskEdit{{Task: &changed}}, reload, true); err != nil {
			t.Fatal(err)
		}
		if err := db.Take(&got, first.Id).Error; err != nil {
			t.Fatal(err)
		}
		if seen[got.PinnedGeneration] || got.Reference != "gateway" || got.Clients[0] != members[0] {
			t.Fatalf("edit did not fence or preserve fields: %+v", got)
		}
		seen[got.PinnedGeneration] = true
	}
	if reloads != 2 {
		t.Fatalf("reload count = %d", reloads)
	}
}
