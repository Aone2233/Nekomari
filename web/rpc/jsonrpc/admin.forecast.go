package jsonrpc

import (
	"context"
	"sort"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/cycle"
	"github.com/Aone2233/nekomari/internal/forecast"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils/notifier"
)

// admin.forecast.go
// Traffic projections for the panel.
//
// The projection itself is `internal/forecast`, which refuses to project from too little data; the cycle
// it projects across is `internal/cycle`, which mirrors the agent's reset rule. This file is the wiring
// and the presentation: it reports each node's projection *with its basis*, and reports the refusals with
// their reasons rather than leaving a blank.
//
// Every row carries the forecast's own explanation, because a projected number without one is not
// actionable: "you will use 300 GB" cannot be argued with, while "300 GB, from 41 samples over 33% of the
// cycle, ±7%" can.

func init() {
	RegisterWithGroupAndMeta("getTrafficForecast", rpc.RoleAdmin, adminGetTrafficForecast,
		&rpc.MethodMeta{
			Name:    "admin:getTrafficForecast",
			Summary: "Per-node traffic projection for the current cycle, with the basis for each",
			Params: []rpc.ParamMeta{
				{Name: "reset_day", Type: "number", Required: false, Description: "Cycle day; read from settings when omitted"},
			},
			Returns: "rows plus the cycle window and the threshold used",
		})
	RegisterWithGroupAndMeta("setTrafficCycleDay", rpc.RoleAdmin, adminSetTrafficCycleDay,
		&rpc.MethodMeta{
			Name:    "admin:setTrafficCycleDay",
			Summary: "Set the day of the month the fleet's traffic cycle resets",
			Params: []rpc.ParamMeta{
				{Name: "reset_day", Type: "number", Required: true, Description: "1..31, matching the agent's --month-rotate"},
			},
			Returns: "the stored value",
		})
}

// forecastRow is one node's projection as the page reads it.
type forecastRow struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`

	// Method and Reason carry the forecast's own account of itself. `insufficient` is a first-class
	// answer, not an error: the page shows the reason rather than an empty cell, because "not enough data"
	// and "no problem" look identical otherwise.
	Method string `json:"method"`
	Reason string `json:"reason,omitempty"`

	UsedBytes      int64 `json:"used_bytes"`
	ProjectedBytes int64 `json:"projected_bytes"`
	LimitBytes     int64 `json:"limit_bytes"`
	// LimitType is reported because it decides what the numbers mean: a `max` node's projection is not
	// the sum of its two directions.
	LimitType string `json:"limit_type"`

	// LimitSet is false for a node with no allowance, which is projected but not against infinity.
	LimitSet          bool    `json:"limit_set"`
	ProjectedFraction float64 `json:"projected_fraction"`
	WouldExceed       bool    `json:"would_exceed"`

	CrossesAt      string  `json:"crosses_at,omitempty"`
	CrossesInDays  float64 `json:"crosses_in_days,omitempty"`
	CrossesInCycle bool    `json:"crosses_in_cycle"`

	Basis forecast.Basis `json:"basis"`
	// Warnings is how many forecast warnings this node has been sent this cycle, so an operator can see
	// that a notification went out rather than having to check the log.
	Warning *forecast.Warning `json:"warning,omitempty"`
}

func adminGetTrafficForecast(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ResetDay int `json:"reset_day"`
	}
	_ = req.BindParams(&params)

	day := params.ResetDay
	if day == 0 {
		day = configuredResetDay()
	}
	// The page is allowed to ask about a day other than the stored one, so an operator can see what a
	// change would do before making it. An impossible day is refused rather than silently defaulted.
	if day < 1 || day > 31 {
		return nil, rpc.MakeError(rpc.InvalidParams, "reset_day must be between 1 and 31", nil)
	}

	window, err := cycle.Bounds(day, time.Local, time.Now())
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	allClients, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list nodes: "+err.Error(), nil)
	}

	rows := make([]forecastRow, 0, len(allClients))
	for _, client := range allClients {
		rows = append(rows, forecastRowFor(client, window, day))
	}
	// Worth-attention first: a node projected to exceed its limit, then one whose crossing is in this
	// cycle, then the rest by how full they are. A page that listed by uuid would bury the one row the
	// operator opened it for.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].WouldExceed != rows[j].WouldExceed {
			return rows[i].WouldExceed
		}
		if rows[i].CrossesInCycle != rows[j].CrossesInCycle {
			return rows[i].CrossesInCycle
		}
		return rows[i].ProjectedFraction > rows[j].ProjectedFraction
	})

	return map[string]any{
		"rows": rows,
		"cycle": map[string]any{
			"start":     window.Start.Format(time.RFC3339),
			"end":       window.End.Format(time.RFC3339),
			"reset_day": window.ResetDay,
			"location":  window.Location,
		},
		"threshold": notifier.ForecastThreshold(),
	}, nil
}

// forecastRowFor projects one node, or reports why it could not be.
func forecastRowFor(client models.Client, window cycle.Cycle, day int) forecastRow {
	row := forecastRow{
		UUID:      client.UUID,
		Name:      client.Name,
		LimitBytes: client.TrafficLimit,
		LimitType: client.TrafficLimitType,
		LimitSet:  client.TrafficLimit > 0,
	}
	projection, err := notifier.ProjectClient(time.Now(), client, day)
	if err != nil {
		// A read failure is reported per row rather than failing the whole page: one node whose series
		// cannot be read should not hide the nine that can.
		row.Method = string(forecast.MethodInsufficient)
		row.Reason = err.Error()
		return row
	}
	row.Method = string(projection.Method)
	row.Reason = projection.Reason
	row.UsedBytes = projection.UsedBytes
	row.ProjectedBytes = projection.ProjectedBytes
	row.LimitSet = projection.LimitSet
	row.ProjectedFraction = projection.ProjectedFraction
	row.WouldExceed = projection.WouldExceed
	row.CrossesInDays = projection.CrossesInDays
	row.CrossesInCycle = projection.CrossesInCycle
	row.Basis = projection.Basis
	row.Warning = projection.Warning
	if !projection.CrossesAt.IsZero() {
		row.CrossesAt = projection.CrossesAt.Format(time.RFC3339)
	}
	return row
}

func adminSetTrafficCycleDay(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ResetDay int `json:"reset_day"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	// Refused rather than clamped: a day the calendar cannot have is a typo, and silently storing the 1st
	// would leave the operator believing they had configured the 31st.
	if params.ResetDay < 1 || params.ResetDay > 31 {
		return nil, rpc.MakeError(rpc.InvalidParams, "reset_day must be between 1 and 31", nil)
	}
	db := dbcore.GetDBInstance()
	if db == nil {
		return nil, rpc.MakeError(rpc.InternalError, "database not initialized", nil)
	}
	if err := config.Set(config.TrafficMonthRotateKey, params.ResetDay); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to store the cycle day: "+err.Error(), nil)
	}
	return map[string]any{"reset_day": params.ResetDay}, nil
}

// configuredResetDay reads the stored cycle day, defaulting to the 1st.
func configuredResetDay() int {
	day, err := config.GetAs[int](config.TrafficMonthRotateKey, 1)
	if err != nil || day < 1 || day > 31 {
		return 1
	}
	return day
}
