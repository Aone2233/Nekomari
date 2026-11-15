package plugin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/rpc"
)

// An unload hook can start an asynchronous admin RPC and return before that
// RPC finishes. Quiescence must cancel and join it even if the handler ignores
// cancellation and the plugin runtime has already closed.
func TestPluginShutdownCancelsAndJoinsUnawaitedRPC(t *testing.T) {
	withTempDataDir(t)
	started, canceled, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	if err := rpc.Register("plugin:testShutdownBlocked", func(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		close(finished)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	defer rpc.Unregister("plugin:testShutdownBlocked")
	zip := writePluginZip(t, map[string]string{
		"komari-plugin.json": demoManifest,
		"script.js": `
			const server = require("server");
			function load() {}
			function unload() { server.call("plugin:testShutdownBlocked"); }
		`,
	})
	if _, err := InstallZip(zip); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("demo", true, true); err != nil {
		t.Fatal(err)
	}
	if err := global.closeAll(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("unawaited RPC did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	err := global.async.stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("quiescence did not wait for the RPC: %v", err)
	}
	select {
	case <-canceled:
	default:
		close(release)
		t.Fatal("quiescence did not cancel the RPC")
	}
	if _, _, ok := global.async.start(); ok {
		close(release)
		t.Fatal("new RPC accepted after quiescence")
	}
	select {
	case <-finished:
		close(release)
		t.Fatal("fixture RPC finished before release")
	default:
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture RPC did not finish")
	}
	if err := global.async.stop(t.Context()); err != nil {
		t.Fatalf("retry waiting for RPC: %v", err)
	}
}

func TestAsyncCallsQuiescenceOrdersConcurrentStartsAndCompletion(t *testing.T) {
	var calls asyncCalls
	var started sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		started.Add(1)
		go func() {
			defer started.Done()
			<-start
			ctx, done, ok := calls.start()
			if ok {
				<-ctx.Done()
				done()
				done()
			}
		}()
	}
	close(start)
	if err := calls.stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	started.Wait()
	if _, _, ok := calls.start(); ok {
		t.Fatal("call started after shutdown")
	}
	if err := calls.stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestPluginShutdownDeadlineIncludesUnload(t *testing.T) {
	withTempDataDir(t)
	zip := writePluginZip(t, map[string]string{
		"komari-plugin.json": demoManifest,
		"script.js": `function load() {} function unload() {
			return new Promise(resolve => setTimeout(resolve, 200));
		}`,
	})
	if _, err := InstallZip(zip); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("demo", true, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	err := global.closeAllContext(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown returned before unload completed: %v", err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := global.closeAllContext(ctx); err != nil {
		t.Fatalf("shutdown retry: %v", err)
	}
}
