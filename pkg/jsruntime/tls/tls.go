package tlsmodule

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"

	"github.com/Aone2233/nekomari/pkg/jsruntime/events"
	"github.com/Aone2233/nekomari/pkg/jsruntime/internal/bridge"
	netmodule "github.com/Aone2233/nekomari/pkg/jsruntime/net"
	"github.com/dop251/goja"
)

type Module struct {
	runtime *bridge.Runtime
	net     *netmodule.Module
}

func New(runtime *bridge.Runtime, network *netmodule.Module) *Module {
	return &Module{runtime, network}
}
func (m *Module) Load(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	connect := func(call goja.FunctionCall) goja.Value { return m.connect(vm, call) }
	_ = exports.Set("connect", connect)
	_ = exports.Set("createConnection", connect)
	_ = module.Set("exports", exports)
}
func (m *Module) connect(vm *goja.Runtime, call goja.FunctionCall) *goja.Object {
	options, ok := call.Argument(0).(*goja.Object)
	if !ok {
		panic(vm.NewTypeError("tls.connect requires an options object"))
	}
	stringOpt := func(key string) string {
		v := options.Get(key)
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			return ""
		}
		return v.String()
	}
	host := stringOpt("host")
	serverName := stringOpt("servername")
	if serverName == "" {
		serverName = host
	}
	port := 0
	if v := options.Get("port"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		port = int(v.ToInteger())
	}
	var source *goja.Object
	if v := options.Get("socket"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		source, ok = v.(*goja.Object)
		if !ok {
			panic(vm.NewTypeError("tls.connect socket must be a net.Socket"))
		}
	}
	if serverName == "" || (source == nil && (host == "" || port < 1 || port > 65535)) {
		panic(vm.NewTypeError("tls.connect requires servername and a valid socket or host and port"))
	}
	cfg := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if ca := stringOpt("ca"); ca != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(ca)) {
			panic(vm.NewTypeError("invalid TLS CA"))
		}
		cfg.RootCAs = roots
	}
	if source != nil {
		if err := m.net.PrepareDetachSocket(source); err != nil {
			panic(vm.NewGoError(err))
		}
	}
	socket := events.NewEmitter(vm)
	_ = socket.Set("connecting", true)
	_ = socket.Set("encrypted", true)
	_ = socket.Set("authorized", false)
	ctx, cancel := context.WithTimeout(context.Background(), m.runtime.Timeout())
	resource := m.runtime.AddResource(cancel)
	if resource == 0 {
		cancel()
		return socket
	}
	_ = socket.Set("destroy", func() *goja.Object { cancel(); return socket })
	if callback, ok := goja.AssertFunction(call.Argument(1)); ok {
		once, _ := goja.AssertFunction(socket.Get("once"))
		_, _ = once(socket, vm.ToValue("secureConnect"), vm.ToValue(callback))
	}
	go func() {
		transferred := false
		defer func() {
			if !transferred {
				cancel()
				m.runtime.RemoveResource(resource)
			}
		}()
		var conn net.Conn
		var err error
		if source != nil {
			conn, err = m.net.DetachSocket(ctx, source)
		} else {
			conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		}
		if err != nil {
			m.emitError(socket, err)
			return
		}
		secure := tls.Client(conn, cfg)
		stop := context.AfterFunc(ctx, func() { _ = secure.Close() })
		err = secure.HandshakeContext(ctx)
		if err != nil {
			_ = secure.Close()
			m.emitError(socket, fmt.Errorf("TLS handshake failed: %w", err))
			return
		}
		if !m.runtime.RunOnLoop(func(vm *goja.Runtime) {
			defer cancel()
			defer m.runtime.RemoveResource(resource)
			if !stop() || ctx.Err() != nil {
				_ = secure.Close()
				return
			}
			_ = m.runtime.RunJob(vm, "tls secureConnect", func() error {
				if !m.net.AttachSocket(vm, socket, secure) {
					_ = secure.Close()
					return fmt.Errorf("TLS runtime closed")
				}
				_ = socket.Set("authorized", true)
				return events.Emit(vm, socket, "secureConnect")
			})
		}) {
			_ = secure.Close()
		} else {
			transferred = true
		}
	}()
	return socket
}
func (m *Module) emitError(socket *goja.Object, err error) {
	m.runtime.RunOnLoop(func(vm *goja.Runtime) {
		_ = m.runtime.RunJob(vm, "tls error", func() error {
			_ = socket.Set("connecting", false)
			return events.Emit(vm, socket, "error", vm.NewGoError(err))
		})
	})
}
