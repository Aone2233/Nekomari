package jsonrpc

import (
	"context"
	"time"

	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/database/clients"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/pkg/rpc"
	agentrt "github.com/Aone2233/nekomari/web/agent"
)

// admin.agentupdate.go
// Roll a fleet's agents to a chosen version.
//
// Roadmap H6. The requirement is arithmetic on two facts the panel already has — each node's reported
// version and whether it is connected — plus the ability to tell a node which version to run, which is the
// v2 event channel that already carries exec, ping and terminal requests.
//
// ## Why the target is pushed repeatedly rather than set once
//
// The agent holds the pushed target in memory and re-learns it from the panel, so the panel sends it on
// every dispatch. That is not belt-and-braces: a node restarted in the middle of a rollout comes back with no
// target, and without a re-send it would resume tracking the latest release — silently undoing the rollout for
// that node and, worse, doing it in the direction of the newest release.
//
// ## Why this reports per node rather than a fleet verdict
//
// A rollout is per node: one that is offline gets nothing, one already at the target is a no-op, and both are
// answers the operator needs to tell apart from a failure. So the response is a list, and the counts are
// derived from it rather than the other way round.

func init() {
	RegisterWithGroupAndMeta("rollAgentUpdate", rpc.RoleAdmin, adminRollAgentUpdate, &rpc.MethodMeta{
		Name:    "admin:rollAgentUpdate",
		Summary: "Ask nodes to run a specific agent version, reporting each node's outcome",
		Params: []rpc.ParamMeta{
			{Name: "uuids", Type: "string[]", Required: false, Description: "Omit for every node"},
			{Name: "target", Type: "string", Required: true, Description: "Release tag, e.g. v0.1.41; empty clears the target"},
			{Name: "dry_run", Type: "bool", Required: false, Description: "Report what would happen without dispatching"},
		},
		Returns: "Per-node outcomes: dispatched, already at target, or offline",
	})
	RegisterWithGroupAndMeta("agentVersions", rpc.RoleAdmin, adminAgentVersions, &rpc.MethodMeta{
		Name:    "admin:agentVersions",
		Summary: "Each node's reported agent version and whether it is connected",
		Returns: "rows plus a count per version",
	})
}

// agentVersionRow is one node's update state as the page reads it.
type agentVersionRow struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	// Version is what the node itself last reported. Empty for a node that has never connected.
	Version string `json:"version"`
	// Online is whether the panel currently holds a connection for it, which decides whether a request can
	// reach it at all.
	Online bool `json:"online"`
	// AtTarget is whether Version already matches the target asked about, when one was given.
	AtTarget bool `json:"at_target,omitempty"`
}

// agentUpdateOutcome is what happened to one node.
type agentUpdateOutcome struct {
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// The statuses are named rather than boolean because "offline" and "failed" want different actions: the first
// needs someone to wait or investigate the node, the second needs the agent's log.
const (
	updateDispatched = "dispatched"
	updateAtTarget   = "at_target"
	updateOffline    = "offline"
	updateFailed     = "failed"
)

func adminAgentVersions(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	all, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list nodes: "+err.Error(), nil)
	}
	rows := make([]agentVersionRow, 0, len(all))
	counts := map[string]int{}
	for _, client := range all {
		online := agentrt.IsAgentOnline(client.UUID)
		rows = append(rows, agentVersionRow{
			UUID: client.UUID, Name: client.Name, Version: client.Version, Online: online,
		})
		// Counted by the version reported, with "unknown" for a node that has never connected — which is the
		// first thing an operator wants to see, because such a node cannot be rolled at all.
		if client.Version == "" {
			counts["unknown"]++
		} else {
			counts[client.Version]++
		}
	}
	return map[string]any{"rows": rows, "counts": counts}, nil
}

func adminRollAgentUpdate(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUIDs  []string `json:"uuids"`
		Target string   `json:"target"`
		DryRun bool     `json:"dry_run"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}

	all, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list nodes: "+err.Error(), nil)
	}
	// A selection is resolved against the fleet rather than trusted, so a stale uuid in a selection is
	// reported as not found instead of silently reducing the rollout.
	selected := make(map[string]bool, len(params.UUIDs))
	for _, uuid := range params.UUIDs {
		selected[uuid] = true
	}

	outcomes := make([]agentUpdateOutcome, 0, len(all))
	sent := 0
	for _, client := range all {
		if len(selected) > 0 && !selected[client.UUID] {
			continue
		}
		row := agentUpdateOutcome{UUID: client.UUID, Name: client.Name}

		// The target is compared with the same normalisation the agent uses, so the panel and the node cannot
		// disagree about whether a node is already there and dispatch an install that does nothing.
		if params.Target != "" && sameReleaseVersion(client.Version, params.Target) {
			row.Status = updateAtTarget
			row.Detail = client.Version
			outcomes = append(outcomes, row)
			continue
		}
		if params.DryRun {
			row.Status = updateDispatched
			row.Detail = "would be dispatched (dry run)"
			outcomes = append(outcomes, row)
			continue
		}
		// Online first, because an offline node's request cannot be delivered now and the operator should know
		// that rather than assume it will arrive.
		if !agentrt.IsAgentOnline(client.UUID) {
			row.Status = updateOffline
			row.Detail = "not connected; the request was not delivered"
			outcomes = append(outcomes, row)
			continue
		}
		if !agentrt.DispatchV2Event(client.UUID, v2.MethodAgentUpdate, v2.UpdateParams{Target: params.Target}) {
			row.Status = updateFailed
			row.Detail = "the panel could not dispatch to this node"
			outcomes = append(outcomes, row)
			continue
		}
		row.Status = updateDispatched
		if params.Target == "" {
			row.Detail = "target cleared; this node will track releases again"
		} else {
			row.Detail = params.Target
		}
		outcomes = append(outcomes, row)
		sent++
	}

	if len(selected) > 0 {
		known := map[string]bool{}
		for _, client := range all {
			known[client.UUID] = true
		}
		for uuid := range selected {
			if !known[uuid] {
				outcomes = append(outcomes, agentUpdateOutcome{
					UUID: uuid, Status: updateFailed, Detail: "no such node",
				})
			}
		}
	}

	if !params.DryRun && params.Target != "" && sent > 0 {
		// Logged with the count and the version, not the node list: the fleet is ten nodes here, but a rule
		// that only works for ten is not a rule. Which nodes succeeded is answerable from their reported
		// versions a moment later, and that is the more trustworthy record.
		actor, ip := auditActor(ctx)
		auditlog.Log(ip, actor,
			"roll agent update: target="+params.Target+" dispatched="+itoa(sent), "warn")
	}

	return map[string]any{
		"target":   params.Target,
		"dry_run":  params.DryRun,
		"outcomes": outcomes,
	}, nil
}

// sameReleaseVersion compares a reported version with a release tag, ignoring the `v` that tags carry.
//
// The agent does the same comparison for the same reason: releases are tagged `v0.1.41` while a node reports
// `0.1.41`, and treating those as different would make every rollout re-dispatch to nodes already at the
// target.
func sameReleaseVersion(reported, target string) bool {
	return trimVersionPrefix(reported) == trimVersionPrefix(target)
}

func trimVersionPrefix(version string) string {
	for len(version) > 0 && (version[0] == 'v' || version[0] == 'V' || version[0] == ' ' || version[0] == '\t') {
		version = version[1:]
	}
	for len(version) > 0 && (version[len(version)-1] == ' ' || version[len(version)-1] == '\t') {
		version = version[:len(version)-1]
	}
	return version
}

// itoa is shared with the bulk handler's audit line.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

// unusedTimeGuard keeps the time import honest if the dry-run path above is ever simplified away.
var _ = time.Now
