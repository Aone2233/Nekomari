package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/dop251/goja"
)

// registerRPC registers a plugin-owned RPC method. The handler is a
// JavaScript function receiving the JSON-RPC params and returning the result
// (or throwing an Error with optional code/data). Called from the plugin's
// own event loop during script evaluation; it takes the manager lock itself.
func (m *Manager) registerRPC(short, method string, handler goja.Callable) error {
	method = strings.TrimSpace(method)
	if method == "" || strings.HasPrefix(method, "rpc.") {
		return fmt.Errorf("invalid RPC method name %q", method)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[short]
	if !ok {
		return fmt.Errorf("plugin %q is not loaded", short)
	}
	inst.mu.Lock()
	if inst.rpcMethods == nil {
		inst.rpcMethods = make(map[string]goja.Callable)
	}
	if _, exists := inst.rpcMethods[method]; exists {
		inst.mu.Unlock()
		return nil // already registered in this load
	}
	inst.rpcMethods[method] = handler
	inst.mu.Unlock()
	if err := rpc.Register(method, m.rpcHandler(short, method)); err != nil {
		inst.mu.Lock()
		delete(inst.rpcMethods, method)
		inst.mu.Unlock()
		return err
	}
	return nil
}

// rpcHandler bridges one registered RPC call into the plugin event loop.
func (m *Manager) rpcHandler(short, method string) rpc.Handler {
	return func(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		inst := m.instanceFor(short)
		if inst == nil {
			return nil, &rpc.JsonRpcError{Code: rpc.Unavailable, Message: fmt.Sprintf("plugin %q is not loaded", short)}
		}
		inst.mu.RLock()
		handler := inst.rpcMethods[method]
		host := inst.host
		alive := inst.runtime != nil
		inst.mu.RUnlock()
		if !alive || handler == nil || host == nil {
			return nil, &rpc.JsonRpcError{Code: rpc.MethodNotFound, Message: "method not found", Data: method}
		}

		type outcome struct {
			result any
			err    *rpc.JsonRpcError
		}
		done := make(chan outcome, 1)
		var once sync.Once
		finish := func(result any, err *rpc.JsonRpcError) { once.Do(func() { done <- outcome{result, err} }) }
		queued := host.RunOnLoop(func(vm *goja.Runtime) {
			runErr := host.RunJob(vm, "plugin rpc "+method, func() error {
				params := vm.ToValue(req.Params)
				if params == nil {
					params = goja.Null()
				}
				value, callErr := handler(goja.Undefined(), params)
				if callErr != nil {
					finish(nil, jsErrorToRPCError(vm, callErr))
					return nil // error is captured, do not double-report
				}
				resolve := func(value goja.Value) {
					exported, exportErr := exportJSValue(value)
					if exportErr != nil {
						finish(nil, &rpc.JsonRpcError{Code: rpc.InternalError, Message: exportErr.Error()})
					} else {
						finish(exported, nil)
					}
				}
				if _, ok := value.Export().(*goja.Promise); ok {
					then, callable := goja.AssertFunction(value.ToObject(vm).Get("then"))
					if !callable {
						finish(nil, &rpc.JsonRpcError{Code: rpc.InternalError, Message: "plugin Promise has no then method"})
						return nil
					}
					_, thenErr := then(value, vm.ToValue(func(call goja.FunctionCall) goja.Value {
						resolve(call.Argument(0))
						return goja.Undefined()
					}), vm.ToValue(func(call goja.FunctionCall) goja.Value {
						finish(nil, jsValueToRPCError(call.Argument(0)))
						return goja.Undefined()
					}))
					if thenErr != nil {
						finish(nil, jsErrorToRPCError(vm, thenErr))
					}
				} else {
					resolve(value)
				}
				return nil
			})
			if runErr != nil {
				finish(nil, jsErrorToRPCError(vm, runErr))
			}
		})
		if !queued {
			return nil, &rpc.JsonRpcError{Code: rpc.Unavailable, Message: "plugin runtime is closed"}
		}
		timer := time.NewTimer(host.Timeout())
		defer timer.Stop()
		select {
		case result := <-done:
			return result.result, result.err
		case <-ctx.Done():
			return nil, &rpc.JsonRpcError{Code: rpc.DeadlineExceeded, Message: "plugin RPC request canceled"}
		case <-timer.C:
			return nil, &rpc.JsonRpcError{Code: rpc.DeadlineExceeded, Message: "plugin RPC handler timed out"}
		}
	}
}

// exportJSValue exports a goja value into a JSON-RPC result. Circular
// structures and other export failures become an error instead of panicking
// inside the event loop.
func exportJSValue(value goja.Value) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("export JavaScript value: %v", recovered)
		}
	}()
	return value.Export(), nil
}

// jsErrorToRPCError converts a thrown JavaScript error into a JSON-RPC error,
// honoring optional code/data properties.
func jsErrorToRPCError(vm *goja.Runtime, err error) *rpc.JsonRpcError {
	var exception *goja.Exception
	if errors.As(err, &exception) {
		return jsValueToRPCError(exception.Value())
	}
	return &rpc.JsonRpcError{Code: rpc.InternalError, Message: err.Error()}
}

func jsValueToRPCError(value goja.Value) *rpc.JsonRpcError {
	code := rpc.InternalError
	message := value.String()
	var data any
	if obj, ok := value.(*goja.Object); ok {
		if value := obj.Get("code"); value != nil && !goja.IsUndefined(value) {
			if n := value.ToInteger(); n != 0 {
				code = int(n)
			}
		}
		if value := obj.Get("message"); value != nil && !goja.IsUndefined(value) && value.String() != "" {
			message = value.String()
		}
		if value := obj.Get("data"); value != nil && !goja.IsUndefined(value) {
			if exported, exportErr := exportJSValue(value); exportErr == nil {
				data = exported
			}
		}
	}
	return &rpc.JsonRpcError{Code: code, Message: message, Data: data}
}
