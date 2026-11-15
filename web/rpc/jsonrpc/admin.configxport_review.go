package jsonrpc

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var errConfigImportPreviewStale = errors.New("configuration changed or preview is invalid; preview the import again")
var errConfigImportLegacyIdentity = errors.New("schema 1/2 tasks and maintenance windows have only panel-local IDs and cannot be safely imported; recreate them or use an encrypted database backup for recovery")

const maxConfigImportDocumentBytes = 768 << 10

// reviewedDraftConfigImportRPC uses original request bytes and binds each
// import to the document and state reviewed by the caller.
func reviewedDraftConfigImportRPC(ctx context.Context, req *rpc.JsonRpcRequest, db *gorm.DB, preview bool, reload func() error) (any, *rpc.JsonRpcError) {
	raw, err := rawImportDocument(req)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if len(raw) > maxConfigImportDocumentBytes {
		return nil, rpc.MakeError(rpc.InvalidParams, "Configuration document exceeds the import size limit", nil)
	}
	var envelope struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(req.RawJSON(), &envelope); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body", nil)
	}
	var token string
	if encoded, exists := envelope.Params["review_token"]; exists {
		if len(bytes.TrimSpace(encoded)) == 0 || bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) || json.Unmarshal(encoded, &token) != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Invalid review token", nil)
		}
	}
	document, err := configxport.Parse(raw)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	audit := &models.Log{UUID: actor, IP: ip, Message: "import configuration", MsgType: "warn"}
	plan, err := reviewedDraftImportConfigWithAudit(ctx, db, document, preview, token, reload, audit)
	if errors.Is(err, errConfigImportPreviewStale) {
		return nil, rpc.MakeLocalizedError(rpc.InvalidParams, errConfigImportPreviewStale.Error(), "config_import_review_stale")
	}
	if errors.Is(err, errConfigImportLegacyIdentity) {
		return nil, rpc.MakeLocalizedError(rpc.InvalidParams, errConfigImportLegacyIdentity.Error(), "config_import_legacy_identity")
	}
	var pending *configImportReloadError
	if errors.As(err, &pending) {
		return nil, rpc.MakeCommittedError(rpc.Unavailable, "Configuration was saved, but the ping schedule did not reload. Do not retry; refresh and contact the administrator.", "config_import_schedule_pending", 0)
	}
	if err != nil {
		// Do not send SQL errors or secret-bearing values back to a caller.
		return nil, rpc.MakeError(rpc.InvalidParams, "Configuration preview or import was rejected", nil)
	}
	return plan, nil
}

var configImportReviewKey struct {
	sync.Once
	value [32]byte
	err   error
}

// A keyed digest avoids exposing a verifier for node tokens or private
// settings. The process-local key invalidates all previews after restart.
func configImportReviewToken(tx *gorm.DB, document configxport.Document) (string, error) {
	if tx == nil {
		return "", errors.New("configuration transaction unavailable")
	}
	configImportReviewKey.Do(func() { _, configImportReviewKey.err = rand.Read(configImportReviewKey.value[:]) })
	if configImportReviewKey.err != nil {
		return "", errors.New("configuration review key unavailable")
	}
	quiet := tx.Session(&gorm.Session{Logger: logger.Discard})
	var clients []models.Client
	var tasks []models.PingTask
	var deletions []models.PingTaskDeletion
	var windows []models.MaintenanceWindow
	var notifications []models.OfflineNotification
	var settings []config.ConfigItem
	for _, query := range []struct {
		dest any
	}{
		{&clients}, {&tasks}, {&deletions}, {&windows}, {&notifications}, {&settings},
	} {
		if err := quiet.Find(query.dest).Error; err != nil {
			return "", errors.New("cannot inspect configuration for preview")
		}
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].UUID < clients[j].UUID })
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Id < tasks[j].Id })
	sort.Slice(deletions, func(i, j int) bool { return deletions[i].TaskID < deletions[j].TaskID })
	sort.Slice(windows, func(i, j int) bool { return windows[i].ID < windows[j].ID })
	sort.Slice(notifications, func(i, j int) bool { return notifications[i].Client < notifications[j].Client })
	sort.Slice(settings, func(i, j int) bool { return settings[i].Key < settings[j].Key })
	// Most Client fields are telemetry updated by the Agent. Only its identity,
	// credential and imported configuration participate in the review, so an
	// unrelated heartbeat does not continually invalidate the operator's plan.
	clientRecords := make([]map[string]any, 0, len(clients))
	fields := append(append([]string{}, exportClientFields...), "token")
	type addressFamilyState struct {
		UUID    string
		HasIPv4 bool
		HasIPv6 bool
	}
	families := make([]addressFamilyState, 0, len(clients))
	for _, client := range clients {
		record, err := toRecord(client, fields)
		if err != nil {
			return "", errors.New("cannot inspect configuration for preview")
		}
		clientRecords = append(clientRecords, record)
		// Legacy hostname validation uses family presence, not address values.
		families = append(families, addressFamilyState{client.UUID, strings.TrimSpace(client.IPv4) != "", strings.TrimSpace(client.IPv6) != ""})
	}
	type taskState struct {
		models.PingTask
		ConfigUID        string `json:"config_uid"`
		PinnedGeneration string `json:"pinned_generation"`
	}
	taskRecords := make([]taskState, 0, len(tasks))
	for _, task := range tasks {
		taskRecords = append(taskRecords, taskState{task, task.ConfigUID, task.PinnedGeneration})
	}
	type windowState struct {
		models.MaintenanceWindow
		ConfigUID string `json:"config_uid"`
	}
	windowRecords := make([]windowState, 0, len(windows))
	for _, window := range windows {
		windowRecords = append(windowRecords, windowState{window, window.ConfigUID})
	}
	type notificationState struct {
		Client      string `json:"client"`
		Enable      bool   `json:"enable"`
		GracePeriod int    `json:"grace_period"`
	}
	notificationRecords := make([]notificationState, 0, len(notifications))
	for _, notification := range notifications {
		notificationRecords = append(notificationRecords, notificationState{notification.Client, notification.Enable, notification.GracePeriod})
	}
	state := struct {
		Document      configxport.Document
		Clients       []map[string]any
		Families      []addressFamilyState
		Tasks         []taskState
		Deletions     []models.PingTaskDeletion
		Windows       []windowState
		Notifications []notificationState
		Settings      []config.ConfigItem
	}{document, clientRecords, families, taskRecords, deletions, windowRecords, notificationRecords, settings}
	mac := hmac.New(sha256.New, configImportReviewKey.value[:])
	if err := json.NewEncoder(mac).Encode(state); err != nil {
		return "", fmt.Errorf("encode configuration preview: %w", err)
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func validConfigImportReview(provided, current string) bool {
	if len(provided) != sha256.Size*2 || len(current) != sha256.Size*2 {
		return false
	}
	return hmac.Equal([]byte(provided), []byte(current))
}
