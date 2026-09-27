package jsonrpc

import (
	"context"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	core "github.com/Aone2233/nekomari/internal/maintenance"
	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils/notifier"
)

// admin.maintenance.go
// Maintenance windows: scheduled periods in which a node's alerts are suppressed.
//
// Two things this file is careful about, both of which are the feature's whole value:
//
//   - **Suppression is scoped to notification.** Nothing here touches collection. The metrics
//     keep recording through a window, because the point of the window is that the node is doing
//     something real; a gap in the charts afterwards would be a lie about what happened.
//   - **The cached window list is invalidated on every write**, so an operator who creates a
//     window and immediately reboots a node gets suppression rather than a 30-second wait. The
//     cache exists to keep a per-connection database read off the notifier's hot path.

func init() {
	RegisterWithGroupAndMeta("listMaintenanceWindows", rpc.RoleAdmin, adminListMaintenance,
		&rpc.MethodMeta{
			Name:    "admin:listMaintenanceWindows",
			Summary: "List maintenance windows, with whether each is open now",
			Returns: "{ windows: Window[]; active: string[] }",
		})
	RegisterWithGroupAndMeta("saveMaintenanceWindow", rpc.RoleAdmin, adminSaveMaintenance,
		&rpc.MethodMeta{
			Name:    "admin:saveMaintenanceWindow",
			Summary: "Create or update a maintenance window",
			Params: []rpc.ParamMeta{
				{Name: "id", Type: "number", Required: false, Description: "Omit to create"},
				{Name: "name", Type: "string", Required: true},
				{Name: "start", Type: "string", Required: true, Description: "RFC3339 with a timezone"},
				{Name: "end", Type: "string", Required: true, Description: "RFC3339 with a timezone"},
				{Name: "clients", Type: "string[]", Required: false, Description: "Omit or empty for every node"},
				{Name: "reason", Type: "string", Required: false},
			},
			Returns: "Window",
		})
	RegisterWithGroupAndMeta("deleteMaintenanceWindow", rpc.RoleAdmin, adminDeleteMaintenance,
		&rpc.MethodMeta{
			Name:    "admin:deleteMaintenanceWindow",
			Summary: "Delete a maintenance window",
			Params: []rpc.ParamMeta{
				{Name: "id", Type: "number", Required: true},
			},
			Returns: "null",
		})
}

type maintenanceWindowParams struct {
	ID      uint     `json:"id"`
	Name    string   `json:"name"`
	Start   string   `json:"start"`
	End     string   `json:"end"`
	Clients []string `json:"clients"`
	Reason  string   `json:"reason"`
}

type maintenanceWindowView struct {
	ID      uint     `json:"id"`
	Name    string   `json:"name"`
	Start   string   `json:"start"`
	End     string   `json:"end"`
	Clients []string `json:"clients"`
	Reason  string   `json:"reason"`
	// Open is whether the window covers now for at least one node, and CoversEverything is
	// whether it is fleet-wide. Both are computed server-side so the page and the notifier
	// cannot disagree about what "open" means.
	Open             bool `json:"open"`
	CoversEverything bool `json:"covers_everything"`
	// RemainingSeconds is how long the soonest-ending now-open window has left.
	RemainingSeconds int `json:"remaining_seconds"`
}

func adminListMaintenance(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	db := dbcore.GetDBInstance()
	if db == nil {
		return nil, rpc.MakeError(rpc.InternalError, "database not initialized", nil)
	}
	var rows []models.MaintenanceWindow
	if err := db.Order("start asc, id asc").Find(&rows).Error; err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list maintenance windows: "+err.Error(), nil)
	}

	now := time.Now().UTC()
	windows := make([]maintenanceWindowView, 0, len(rows))
	active := make([]string, 0)
	for _, row := range rows {
		view := maintenanceWindowView{
			ID:               row.ID,
			Name:             row.Name,
			Start:            row.Start.UTC().Format(time.RFC3339),
			End:              row.End.UTC().Format(time.RFC3339),
			Clients:          []string(row.Clients),
			Reason:           row.Reason,
			CoversEverything: len(row.Clients) == 0,
		}
		// Evaluated with no client uuid when the window is fleet-wide, and against its own
		// client list otherwise: `Covers` treats an empty list as every node, so asking about a
		// single uuid would report a scoped window as closed while it is open for others.
		probe := ""
		if len(row.Clients) > 0 {
			probe = row.Clients[0]
		}
		status := core.Evaluate(now, probe, []core.Window{{
			ID: row.ID, Name: row.Name, Start: row.Start.UTC(), End: row.End.UTC(),
			Clients: []string(row.Clients),
		}})
		view.Open = status.Active
		if status.Active {
			view.RemainingSeconds = int(status.Remaining.Seconds())
			active = append(active, row.Name)
		}
		windows = append(windows, view)
	}
	return map[string]any{"windows": windows, "active": active}, nil
}

func adminSaveMaintenance(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params maintenanceWindowParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if params.Name == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "name is required", nil)
	}

	start, err := parseMaintenanceTime("start", params.Start)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	end, err := parseMaintenanceTime("end", params.End)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	// A window that ends before it starts would never be open, and reporting that as saved would
	// leave an operator believing suppression is in place.
	if !end.After(start) {
		return nil, rpc.MakeError(rpc.InvalidParams, "end must be after start", nil)
	}

	db := dbcore.GetDBInstance()
	if db == nil {
		return nil, rpc.MakeError(rpc.InternalError, "database not initialized", nil)
	}

	row := models.MaintenanceWindow{
		ID:      params.ID,
		Name:    params.Name,
		Start:   start,
		End:     end,
		Clients: params.Clients,
		Reason:  params.Reason,
	}
	if params.ID == 0 {
		if err := db.Create(&row).Error; err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to create the window: "+err.Error(), nil)
		}
	} else {
		// Updates with a map so an emptied `clients` list is written rather than skipped: GORM's
		// struct update ignores zero values, and "no longer scoped to those nodes" is a zero
		// value that has to land.
		result := db.Model(&models.MaintenanceWindow{}).Where("id = ?", params.ID).Updates(map[string]any{
			"name": row.Name, "start": row.Start, "end": row.End,
			"clients": row.Clients, "reason": row.Reason,
		})
		if result.Error != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to update the window: "+result.Error.Error(), nil)
		}
		if result.RowsAffected == 0 {
			return nil, rpc.MakeError(rpc.InvalidParams, "no such maintenance window", nil)
		}
	}

	// The notifier's cache would otherwise keep suppressing by the old list for up to its TTL.
	notifier.InvalidateMaintenanceWindows()
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "save maintenance window:"+row.Name, "warn")

	return maintenanceWindowView{
		ID: row.ID, Name: row.Name,
		Start: row.Start.UTC().Format(time.RFC3339), End: row.End.UTC().Format(time.RFC3339),
		Clients: []string(row.Clients), Reason: row.Reason,
		CoversEverything: len(row.Clients) == 0,
	}, nil
}

func adminDeleteMaintenance(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ID uint `json:"id"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if params.ID == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "id is required", nil)
	}
	db := dbcore.GetDBInstance()
	if db == nil {
		return nil, rpc.MakeError(rpc.InternalError, "database not initialized", nil)
	}
	result := db.Delete(&models.MaintenanceWindow{}, params.ID)
	if result.Error != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to delete the window: "+result.Error.Error(), nil)
	}
	if result.RowsAffected == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "no such maintenance window", nil)
	}
	notifier.InvalidateMaintenanceWindows()
	return nil, nil
}

// parseMaintenanceTime accepts RFC3339 with a timezone, and refuses a naive timestamp.
//
// A bare "2026-09-27 20:00" is ambiguous by hours in a fleet that spans them, and a window that
// starts at the wrong time is a window that does not suppress what it was created for.
func parseMaintenanceTime(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, &maintenanceParamError{field + " is required"}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, &maintenanceParamError{field + " must be RFC3339 with a timezone: " + err.Error()}
	}
	return parsed.UTC(), nil
}

type maintenanceParamError struct{ message string }

func (e *maintenanceParamError) Error() string { return e.message }
