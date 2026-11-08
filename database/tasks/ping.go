package tasks

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"github.com/Aone2233/nekomari/utils"
	logger "github.com/Aone2233/nekomari/utils/log"
	"gorm.io/gorm"
)

var (
	pingReloadMu      sync.Mutex
	pingReloadPending bool // protected by pingReloadMu
)

// ScheduleReloadPendingError means the database mutation has already
// committed. Callers must not retry a create blindly: TaskID identifies the
// saved task, while the schedule still needs reconciliation.
type ScheduleReloadPendingError struct {
	TaskID uint
	Err    error
}

func (e *ScheduleReloadPendingError) Error() string {
	return "ping task saved but schedule reload failed: " + e.Err.Error()
}

func (e *ScheduleReloadPendingError) Unwrap() error { return e.Err }

// PingDeletionPendingError means the tasks are already deleted. Their IDs
// remain reserved after historical metric cleanup or schedule reload retries.
type PingDeletionPendingError struct{ Err error }

func (e *PingDeletionPendingError) Error() string {
	return "ping tasks deleted but cleanup or schedule reload is pending: " + e.Err.Error()
}

func (e *PingDeletionPendingError) Unwrap() error { return e.Err }

// PingTaskOptions carries the additive v1.8 probe settings. The zero value is
// the legacy single-probe behavior, so existing callers are unchanged.
type PingTaskOptions struct {
	ProbeAttempts       int
	ProbeSpacingMS      int
	ProbeTimeoutMS      int
	HTTPMode            string
	HTTPExpectedStatus  string
	IPVersionPreference string
}

// ValidateProbeOptions validates the additive v1.8 probe fields. Zero/empty is
// the legacy single-probe behavior and is always valid.
func ValidateProbeOptions(task *models.PingTask) error {
	if task == nil {
		return errors.New("invalid ping task")
	}
	if task.ProbeAttempts < 0 || task.ProbeAttempts > 10 {
		return &PingTaskValidationError{Err: errors.New("probe_attempts must be between 0 and 10")}
	}
	if task.ProbeSpacingMS != 0 && (task.ProbeSpacingMS < 50 || task.ProbeSpacingMS > 1000) {
		return &PingTaskValidationError{Err: errors.New("probe_spacing_ms must be 0 or between 50 and 1000")}
	}
	if task.ProbeTimeoutMS != 0 && (task.ProbeTimeoutMS < 100 || task.ProbeTimeoutMS > 30000) {
		return &PingTaskValidationError{Err: errors.New("probe_timeout_ms must be 0 or between 100 and 30000")}
	}
	switch task.HTTPMode {
	case "", "legacy", "status_aware":
	default:
		return &PingTaskValidationError{Err: errors.New("http_mode must be empty, legacy, or status_aware")}
	}
	if len(task.HTTPExpectedStatus) > 128 {
		return &PingTaskValidationError{Err: errors.New("http_expected_status is too long")}
	}
	for _, char := range task.HTTPExpectedStatus {
		if (char < '0' || char > '9') && char != ',' && char != '-' && char != ' ' {
			return &PingTaskValidationError{Err: errors.New("http_expected_status must contain codes, ranges, commas and spaces")}
		}
	}
	switch task.IPVersionPreference {
	case "", "auto", "4", "6":
	default:
		return &PingTaskValidationError{Err: errors.New("ip_version_preference must be empty, auto, 4, or 6")}
	}
	return nil
}

// AddPingTask 创建延迟监测任务。defaultOn 表示新加入的服务器是否自动开启此监测。
func AddPingTask(clients []string, defaultOn bool, name string, target, task_type string, interval int, reference string) (uint, error) {
	return AddPingTaskWithOptions(clients, defaultOn, name, target, task_type, interval, reference, PingTaskOptions{})
}

// AddPingTaskWithOptions creates a task with the additive probe settings. It is
// the only legacy-path constructor that writes the v1.8 columns.
func AddPingTaskWithOptions(clients []string, defaultOn bool, name string, target, taskType string, interval int, reference string, opts PingTaskOptions) (uint, error) {
	return addPingTaskWithOptions(context.Background(), dbcore.GetDBInstance(), clients, defaultOn, name, target, taskType, interval, reference, opts, ReloadPingSchedule)
}

func addPingTaskWith(ctx context.Context, db *gorm.DB, clients []string, defaultOn bool, name string, target, taskType string, interval int, reference string, reload func() error) (uint, error) {
	return addPingTaskWithOptions(ctx, db, clients, defaultOn, name, target, taskType, interval, reference, PingTaskOptions{}, reload)
}

func addPingTaskWithOptions(ctx context.Context, db *gorm.DB, clients []string, defaultOn bool, name string, target, taskType string, interval int, reference string, opts PingTaskOptions, reload func() error) (uint, error) {
	if ctx == nil || db == nil || reload == nil {
		return 0, errors.New("invalid task creation context")
	}
	normalizedClients := normalizePingClients(models.StringArray(clients))
	task := models.PingTask{
		Clients:             normalizedClients,
		DefaultOn:           defaultOn,
		Name:                name,
		Type:                taskType,
		Target:              target,
		Reference:           strings.TrimSpace(reference),
		Interval:            interval,
		ProbeAttempts:       opts.ProbeAttempts,
		ProbeSpacingMS:      opts.ProbeSpacingMS,
		ProbeTimeoutMS:      opts.ProbeTimeoutMS,
		HTTPMode:            opts.HTTPMode,
		HTTPExpectedStatus:  opts.HTTPExpectedStatus,
		IPVersionPreference: opts.IPVersionPreference,
	}
	if err := ValidateProbeOptions(&task); err != nil {
		return 0, err
	}
	err := pingguard.WithTaskCreation(ctx, func() error {
		return pingguard.WithDefaultRoster(ctx, func() error {
			return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := utils.ValidatePingTaskTargetFamilyWithDB(tx, target, defaultOn, normalizedClients); err != nil {
					return &PingTaskValidationError{Err: err}
				}
				if err := tx.Create(&task).Error; err != nil {
					return err
				}
				if err := assertPingTaskIDAvailable(tx, task.Id); err != nil {
					return err
				}

				// Append by id to avoid races between concurrent create requests.
				result := tx.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("weight", int(task.Id))
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 0 {
					return gorm.ErrRecordNotFound
				}

				return nil
			})
		})
	})
	if err != nil {
		return 0, err
	}
	if err := reload(); err != nil {
		return task.Id, &ScheduleReloadPendingError{TaskID: task.Id, Err: err}
	}
	return task.Id, nil
}

// addPinnedPingTask persists an incarnation with the task in one transaction.
func addPinnedPingTask(ctx context.Context, db *gorm.DB, task *models.PingTask) error {
	if ctx == nil || db == nil || task == nil || task.Id != 0 || task.PinnedGeneration != "" {
		return &PingTaskValidationError{Err: errors.New("invalid new pinned task")}
	}
	if task.Name == "" || len(task.Name) > 255 || (!task.DefaultOn && len(task.Clients) == 0) || len(task.Reference) > 255 {
		return &PingTaskValidationError{Err: errors.New("invalid pinned task configuration")}
	}
	if err := validatePinnedAssignments(task.Clients); err != nil {
		return &PingTaskValidationError{Err: err}
	}
	if err := utils.ValidatePinnedPingInterval(task.Interval); err != nil {
		return &PingTaskValidationError{Err: err}
	}
	if err := utils.ValidatePinnedPingTarget(task.Target, task.Type, task.TargetFamily); err != nil {
		return &PingTaskValidationError{Err: err}
	}
	generation, err := pingguard.NewGeneration()
	if err != nil {
		return err
	}
	task.Clients = normalizePingClients(task.Clients)
	task.Reference = strings.TrimSpace(task.Reference)
	task.PinnedGeneration = generation
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertPinnedNodesExist(tx, task.Clients); err != nil {
			return err
		}
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		if err := assertPingTaskIDAvailable(tx, task.Id); err != nil {
			return err
		}
		result := tx.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("weight", int(task.Id))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		task.Id = 0
		task.PinnedGeneration = ""
	}
	return err
}

// AddPingTaskPinned creates a family-pinned task using the production DB and
// reloads the schedule after the transaction commits.
func AddPingTaskPinned(ctx context.Context, task *models.PingTask) error {
	return AddPingTaskPinnedDraft(ctx, dbcore.GetDBInstance(), task, ReloadPingSchedule)
}

// AddPingTaskPinnedDraft accepts an explicit DB and scheduler for isolated tests.
func AddPingTaskPinnedDraft(ctx context.Context, db *gorm.DB, task *models.PingTask, reload func() error) error {
	if task == nil || reload == nil {
		return errors.New("pinned schedule reload is required")
	}
	if err := validatePinnedAssignments(task.Clients); err != nil {
		return &PingTaskValidationError{Err: err}
	}
	create := func() error { return addPinnedPingTask(ctx, db, task) }
	withNodes := func() error {
		if len(task.Clients) == 0 {
			return create()
		}
		return clientguard.WithMany(ctx, task.Clients, create)
	}
	withRoster := func() error { return pingguard.WithDefaultRoster(ctx, withNodes) }
	if err := pingguard.WithTaskCreation(ctx, withRoster); err != nil {
		return err
	}
	if err := reload(); err != nil {
		return &ScheduleReloadPendingError{TaskID: task.Id, Err: err}
	}
	return nil
}

// editPinnedPingTask requires the caller's task guard. Changing the probe's
// target, protocol, reference or family requires a new task ID: the stored
// metric series do not distinguish historical configurations. Omitting a
// reference or family preserves the persisted value.
func editPinnedPingTask(ctx context.Context, db *gorm.DB, edit PingTaskEdit, family *string) error {
	if ctx == nil || db == nil || edit.Task == nil || edit.Task.Id == 0 {
		return errors.New("invalid pinned task edit")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current models.PingTask
		if err := tx.Take(&current, edit.Task.Id).Error; err != nil {
			return err
		}
		return editPinnedPingTaskTx(tx, current, edit, family)
	})
}

// editPinnedPingTaskTx runs inside the batch transaction and task guard.
func editPinnedPingTaskTx(tx *gorm.DB, current models.PingTask, edit PingTaskEdit, family *string) error {
	if current.TargetFamily == "" || (family != nil && *family != current.TargetFamily) {
		return errors.New("changing target family requires a new task")
	}
	task := edit.Task
	if task.Name == "" || len(task.Name) > 255 {
		return errors.New("invalid pinned task configuration")
	}
	if err := validatePinnedAssignments(task.Clients); err != nil {
		return err
	}
	if err := assertPinnedNodesExist(tx, task.Clients); err != nil {
		return err
	}
	if err := utils.ValidatePinnedPingInterval(task.Interval); err != nil {
		return err
	}
	if err := utils.ValidatePinnedPingTarget(task.Target, task.Type, current.TargetFamily); err != nil {
		return err
	}
	reference := current.Reference
	if edit.Reference != nil {
		reference = strings.TrimSpace(*edit.Reference)
	}
	if len(reference) > 255 {
		return errors.New("pinned reference exceeds limit")
	}
	if task.Target != current.Target || task.Type != current.Type || reference != current.Reference {
		return errors.New("changing a pinned target, protocol or reference requires a new task")
	}
	generation, err := pingguard.NewGeneration()
	if err != nil {
		return err
	}
	updates := map[string]any{
		"name": task.Name, "clients": normalizePingClients(task.Clients), "all_clients": task.DefaultOn,
		"type": task.Type, "target": task.Target, "interval": task.Interval, "reference": reference,
		"pinned_generation": generation,
	}
	return tx.Model(&models.PingTask{}).Where("id = ?", task.Id).Updates(updates).Error
}

// Duplicate assignments waste bounded dispatch slots and may starve other nodes.
func validatePinnedAssignments(clients models.StringArray) error {
	if len(clients) > 4096 {
		return errors.New("too many pinned task assignments")
	}
	seen := make(map[string]struct{}, len(clients))
	for _, id := range clients {
		if id == "" || len(id) > 64 || strings.TrimSpace(id) != id {
			return errors.New("invalid pinned task assignment")
		}
		if _, exists := seen[id]; exists {
			return errors.New("duplicate pinned task assignment")
		}
		seen[id] = struct{}{}
	}
	return nil
}

// Called within the mutation transaction while the public draft entry points
// hold the assigned node guards ahead of any task guard.
func assertPinnedNodesExist(tx *gorm.DB, clients models.StringArray) error {
	if len(clients) == 0 {
		return nil
	}
	var count int64
	if err := tx.Model(&models.Client{}).Where("uuid IN ?", []string(clients)).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(clients)) {
		return &PingTaskValidationError{Err: errors.New("assigned node does not exist")}
	}
	return nil
}

func DeletePingTask(id []uint) error {
	return deletePingTasksWith(context.Background(), dbcore.GetDBInstance(), id, metricstore.FlushReportBatch, metricstore.DeletePingRecordsByTask, ReloadPingSchedule)
}

// PingTaskEdit distinguishes omitted optional fields from explicit clears.
type PingTaskEdit struct {
	Task      *models.PingTask
	Reference *string
	Family    *string
}

var errPinnedPingTaskNotReady = errors.New("family-pinned task editing is not available yet")

// EditPingTask 批量更新延迟监测任务配置。
func EditPingTask(edits []PingTaskEdit) error {
	return EditPingTasksPinnedDraft(context.Background(), dbcore.GetDBInstance(), edits, ReloadPingSchedule)
}

// EditPingTasksPinnedDraft accepts an explicit DB and scheduler for isolated tests.
func EditPingTasksPinnedDraft(ctx context.Context, db *gorm.DB, edits []PingTaskEdit, reload func() error) error {
	nodes := make([]string, 0)
	for _, edit := range edits {
		if edit.Task == nil {
			return errors.New("invalid ping task edit")
		}
		if err := validatePinnedAssignments(edit.Task.Clients); err != nil {
			return &PingTaskValidationError{Err: err}
		}
		nodes = append(nodes, edit.Task.Clients...)
	}
	apply := func() error { return editPingTasksWith(ctx, db, edits, reload, true) }
	return pingguard.WithDefaultRoster(ctx, func() error {
		if len(nodes) == 0 {
			return apply()
		}
		return clientguard.WithMany(ctx, nodes, apply)
	})
}

// editPingTasksWith handles legacy and pinned edits in a single transaction.
func editPingTasksWith(ctx context.Context, db *gorm.DB, edits []PingTaskEdit, reload func() error, allowPinned bool) error {
	if ctx == nil || db == nil || reload == nil || len(edits) == 0 {
		return errors.New("invalid ping task edit batch")
	}
	ids := make([]uint, 0, len(edits))
	seen := make(map[uint]struct{}, len(edits))
	for _, edit := range edits {
		if edit.Task == nil || edit.Task.Id == 0 {
			return errors.New("invalid ping task ID")
		}
		if err := ValidateProbeOptions(edit.Task); err != nil {
			return err
		}
		if _, exists := seen[edit.Task.Id]; exists {
			return errors.New("duplicate ping task ID")
		}
		seen[edit.Task.Id] = struct{}{}
		ids = append(ids, edit.Task.Id)
	}
	return pingguard.WithMany(ctx, ids, func() error {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			currentTasks := make(map[uint]models.PingTask, len(edits))
			for _, edit := range edits {
				var current models.PingTask
				if err := tx.Take(&current, edit.Task.Id).Error; err != nil {
					return err
				}
				if current.TargetFamily == "" && ((edit.Family != nil && *edit.Family != "") || edit.Task.TargetFamily != "") {
					return errors.New("changing target family requires a new task")
				}
				if current.TargetFamily != "" && edit.Task.TargetFamily != "" && edit.Task.TargetFamily != current.TargetFamily {
					return errors.New("changing target family requires a new task")
				}
				if current.TargetFamily != "" && !allowPinned {
					return errPinnedPingTaskNotReady
				}
				currentTasks[current.Id] = current
			}
			for _, edit := range edits {
				task := edit.Task
				current := currentTasks[task.Id]
				if current.TargetFamily != "" {
					if err := editPinnedPingTaskTx(tx, current, edit, edit.Family); err != nil {
						return err
					}
					continue
				}
				// Validate under the task guard after reading the persisted mode.
				// An older caller may omit target_family on a pinned task, so it
				// cannot safely choose the validator before this transaction.
				if err := utils.ValidatePingTaskTargetFamilyWithDB(tx, task.Target, task.DefaultOn, task.Clients); err != nil {
					return &PingTaskValidationError{Err: err}
				}
				updates := map[string]interface{}{
					"name":                  task.Name,
					"clients":               normalizePingClients(task.Clients),
					"all_clients":           task.DefaultOn,
					"type":                  task.Type,
					"target":                task.Target,
					"interval":              task.Interval,
					"probe_attempts":        task.ProbeAttempts,
					"probe_spacing_ms":      task.ProbeSpacingMS,
					"probe_timeout_ms":      task.ProbeTimeoutMS,
					"http_mode":             task.HTTPMode,
					"http_expected_status":  task.HTTPExpectedStatus,
					"ip_version_preference": task.IPVersionPreference,
				}
				if edit.Reference != nil {
					updates["reference"] = strings.TrimSpace(*edit.Reference)
				}
				if err := tx.Model(&models.PingTask{}).Where("id = ?", task.Id).Updates(updates).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := reload(); err != nil {
			return &ScheduleReloadPendingError{Err: err}
		}
		return nil
	})
}

type PingTaskValidationError struct{ Err error }

func (e *PingTaskValidationError) Error() string { return e.Err.Error() }
func (e *PingTaskValidationError) Unwrap() error { return e.Err }

// normalizePingClients 保持 clients 字段序列化为 JSON 数组，避免空值变成 null。
func normalizePingClients(clients models.StringArray) models.StringArray {
	if clients == nil {
		return models.StringArray{}
	}
	return clients
}

func GetAllPingTasks() ([]models.PingTask, error) {
	db := dbcore.GetDBInstance()
	var tasks []models.PingTask
	if err := db.Order("weight ASC").Order("id ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

// GetPingTasksByClient 获取指定服务器需要执行的延迟监测任务。
func GetPingTasksByClient(uuid string) []models.PingTask {
	db := dbcore.GetDBInstance()
	var tasks []models.PingTask
	if err := db.Where("clients LIKE ?", `%"`+uuid+`"%`).Order("weight ASC").Order("id ASC").Find(&tasks).Error; err != nil {
		return nil
	}
	return tasks
}

func UpdatePingTaskOrder(order map[uint]int) error {
	if len(order) == 0 {
		return nil
	}

	db := dbcore.GetDBInstance()
	err := db.Transaction(func(tx *gorm.DB) error {
		// Validate all ids before changing any weights. The update response is
		// not a reliable existence check because some drivers report zero rows
		// when the new value equals the current value.
		ids := make([]uint, 0, len(order))
		for id := range order {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

		var existing int64
		if err := tx.Model(&models.PingTask{}).Where("id IN ?", ids).Count(&existing).Error; err != nil {
			return err
		}
		if existing != int64(len(ids)) {
			return gorm.ErrRecordNotFound
		}

		for _, id := range ids {
			weight := order[id]
			result := tx.Model(&models.PingTask{}).Where("id = ?", id).Update("weight", weight)
			if result.Error != nil {
				return result.Error
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	ReloadPingSchedule()
	return nil
}

// ping 记录已完全迁移到 metric store（指标 ping.latency_ms），运行期读写全部走
// metric store，旧 ping_records 表不再参与。

func DeletePingRecords(id []uint) error {
	return metricstore.DeletePingRecordsByTask(context.Background(), id)
}

func DeleteAllPingRecords() error {
	return metricstore.DeleteAllPingRecords(context.Background())
}

func ReloadPingSchedule() error {
	_, err := reloadPingScheduleWith(false, loadPingSchedule)
	return err
}

// ReconcilePingSchedule retries a failed reload without disturbing healthy
// timers. Startup performs an ordinary reload, so a restart cannot clear a
// failure without attempting to load the persisted configuration again.
func ReconcilePingSchedule() (bool, error) {
	return reloadPingScheduleWith(true, loadPingSchedule)
}

// PingScheduleReloadPending reports whether the last complete reload failed.
// It does not prove individual probes are executing successfully.
func PingScheduleReloadPending() bool {
	pingReloadMu.Lock()
	defer pingReloadMu.Unlock()
	return pingReloadPending
}

func reloadPingScheduleWith(onlyPending bool, reload func() error) (bool, error) {
	// Serialize snapshots and retries so an older snapshot cannot replace a
	// newer one, and a successful reload cannot be followed by a stale retry.
	pingReloadMu.Lock()
	defer pingReloadMu.Unlock()
	if onlyPending && !pingReloadPending {
		return false, nil
	}
	err := reload()
	pingReloadPending = err != nil
	if err != nil {
		logger.Errorf("pingschedule", "Reload failed; reconciliation is pending: %v", err)
	}
	return true, err
}

func loadPingSchedule() error {
	pingTasks, err := GetAllPingTasks()
	if err != nil {
		return err
	}
	return utils.ReloadPingSchedule(pingTasks)
}

// CreateClientWithDefaultPing atomically creates a node and its default-on
// assignments. The caller holds the roster fence and the new node's guard;
// task creation/edit takes the same roster fence before node/task guards.
func CreateClientWithDefaultPing(ctx context.Context, db *gorm.DB, client *models.Client) (bool, error) {
	if ctx == nil || db == nil || client == nil || client.UUID == "" || client.Token == "" {
		return false, errors.New("invalid client creation")
	}
	var ids []uint
	if err := db.WithContext(ctx).Model(&models.PingTask{}).Where("all_clients = ?", true).Pluck("id", &ids).Error; err != nil {
		return false, err
	}
	changed := false
	create := func() error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(client).Error; err != nil {
				return err
			}
			var current []models.PingTask
			if len(ids) != 0 {
				if err := tx.Where("id IN ? AND all_clients = ?", ids, true).Find(&current).Error; err != nil {
					return err
				}
			}
			for _, task := range current {
				if task.AppliesToClient(client.UUID) {
					continue
				}
				next := append(append(models.StringArray{}, task.Clients...), client.UUID)
				if task.TargetFamily != "" {
					if !pingguard.ValidGeneration(task.PinnedGeneration) {
						return errors.New("invalid family-pinned task generation")
					}
					if err := validatePinnedAssignments(next); err != nil {
						return err
					}
				}
				if err := updatePingTaskClients(tx, task, next); err != nil {
					return err
				}
				changed = true
			}
			return nil
		})
	}
	var err error
	if len(ids) == 0 {
		err = create()
	} else {
		err = pingguard.WithMany(ctx, ids, create)
	}
	return err == nil && changed, err
}

// updatePingTaskClients requires the task guard. Keep the generation update
// in the same SQL statement as the assignment so an unassign/reassign cannot
// make an earlier result current again.
func updatePingTaskClients(db *gorm.DB, task models.PingTask, clients models.StringArray) error {
	updates := map[string]any{"clients": normalizePingClients(clients)}
	if task.TargetFamily != "" {
		generation, err := pingguard.NewGeneration()
		if err != nil {
			return err
		}
		updates["pinned_generation"] = generation
	}
	result := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func GetPingRecords(uuid string, taskId int, start, end time.Time) ([]models.PingRecord, error) {
	return metricstore.GetPingRecords(context.Background(), uuid, taskId, start, end)
}

// GetPingRecordsBatch 一次查询取回多个节点的 ping 记录（见 metricstore 里的说明）。
func GetPingRecordsBatch(uuids []string, taskId int, start, end time.Time) (map[string][]models.PingRecord, error) {
	return metricstore.GetPingRecordsBatch(context.Background(), uuids, taskId, start, end)
}
