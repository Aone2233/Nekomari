package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
)

const pinnedResultWriteTimeout = 2 * time.Second

type pinnedResultWriter interface {
	WriteJSONContext(context.Context, interface{}) error
}

// Probe frames are parsed only by this WebSocket's controller. The dispatch
// hook uses launchProbe to consume a session-bound one-use ticket.
func (c *pinnedWSController) handleProbe(data []byte) (bool, error) {
	var frame struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      json.RawMessage `json:"id"`
		Params  json.RawMessage `json:"params"`
	}
	if json.Unmarshal(data, &frame) != nil || frame.Method != v2.MethodAgentPingFamilyProbe {
		return false, nil
	}
	if len(data) > 8192 || frame.JSONRPC != v2.Version || len(frame.ID) != 0 || len(frame.Params) == 0 || bytes.Equal(bytes.TrimSpace(frame.Params), []byte("null")) {
		return true, nil
	}
	var p v2.PingFamilyProbeParams
	decoder := json.NewDecoder(bytes.NewReader(frame.Params))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF {
		return true, nil
	}
	c.mu.Lock()
	dispatch := c.probeDispatch
	c.mu.Unlock()
	if dispatch != nil {
		dispatch(p)
	}
	return true, nil
}

// Execution requires a live session and a matching one-use permit. Panels
// without a compatible permit/result path never send a valid probe frame.
func (c *pinnedWSController) launchPinnedDispatch(p v2.PingFamilyProbeParams) {
	writer, ok := c.conn.(pinnedResultWriter)
	if !ok {
		return
	}
	_ = c.launchProbe(p, func(ctx context.Context) { c.runPinnedProbe(ctx, p, writer) })
}

func (c *pinnedWSController) runPinnedProbe(ctx context.Context, p v2.PingFamilyProbeParams, writer pinnedResultWriter) {
	results := c.probeRunner.probeWithReference(ctx, p.Type, p.Target, p.Reference, p.Family,
		3*time.Second, &c.probeCache, c.probeRunner.probeReference)
	for _, result := range results {
		if ctx.Err() != nil {
			return
		}
		params, err := pinnedProbeResultPayload(p, result)
		if err != nil {
			log.Printf("Pinned-probe result rejected: %v", err)
			return
		}
		writeCtx, cancel := context.WithTimeout(ctx, pinnedResultWriteTimeout)
		err = writer.WriteJSONContext(writeCtx, v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyResult, Params: params})
		cancel()
		if err != nil {
			// Never fall back to POST or a replacement socket for pinned results.
			c.conn.Abort()
			c.clear()
			return
		}
	}
}
