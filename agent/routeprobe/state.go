package routeprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

const Interval = 24 * time.Hour

type State struct {
	Targets  []Target  `json:"targets"`
	ProbedAt time.Time `json:"probed_at"`
	Results  []Result  `json:"results"`
}

// Persist failed attempts as well: restarts must not turn an unreachable route into a scan loop.
func CachedProbe(ctx context.Context, file string, targets []Target, now time.Time, probe func(context.Context, []Target) []Result) (State, error) {
	var state State
	f, err := os.Open(file)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(f, 64*1024+1))
		f.Close()
		if readErr != nil || len(data) > 64*1024 {
			return State{}, fmt.Errorf("cannot read bounded route state")
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return State{}, fmt.Errorf("invalid state; refusing an unscheduled probe: %w", err)
		}
		if state.ProbedAt.IsZero() || len(state.Targets) == 0 || state.ProbedAt.After(now.Add(5*time.Minute)) {
			return State{}, fmt.Errorf("invalid state timestamp or targets; refusing an unscheduled probe")
		}
		if reflect.DeepEqual(state.Targets, targets) && !state.ProbedAt.IsZero() && now.Before(state.ProbedAt.Add(Interval)) {
			return state, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return State{}, fmt.Errorf("read state: %w", err)
	}
	state = State{Targets: targets, ProbedAt: now.UTC()}
	for _, target := range targets {
		state.Results = append(state.Results, Result{Carrier: target.Carrier, Target: target.IP, Error: "previous probe interrupted before results were saved"})
	}
	// Reserve the daily slot before network work; interruption will not cause repeated probes.
	if err := writeState(file, state); err != nil {
		return State{}, fmt.Errorf("persist schedule before probing: %w", err)
	}
	state.Results = probe(ctx, targets)
	return state, writeState(file, state)
}

func writeState(file string, state State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".route-state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, file)
}
