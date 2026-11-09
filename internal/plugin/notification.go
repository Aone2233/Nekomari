package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/jsruntime"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/dop251/goja"
)

type pluginNotificationChannel struct {
	short   string
	host    *jsruntime.Host
	handler goja.Callable
}

func (m *Manager) registerNotificationChannel(inst *Instance, id string, cfg models.Configuration, handler goja.Callable, host *jsruntime.Host) error {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.runtime == nil || inst.host != host {
		return fmt.Errorf("plugin %q runtime is unavailable", inst.info.Short)
	}
	channel := &pluginNotificationChannel{inst.info.Short, host, handler}
	if err := messageSender.RegisterNotificationChannel(id, "plugin:"+inst.info.Short+":"+id, cfg, channel); err != nil {
		return err
	}
	if inst.notificationChannels == nil {
		inst.notificationChannels = map[string]*pluginNotificationChannel{}
	}
	inst.notificationChannels[id] = channel
	return nil
}
func (m *Manager) unregisterNotificationChannels(inst *Instance) {
	inst.mu.Lock()
	ids := inst.notificationChannels
	inst.notificationChannels = nil
	inst.mu.Unlock()
	for id, channel := range ids {
		messageSender.UnregisterNotificationChannel(id, channel)
	}
}
func (c *pluginNotificationChannel) Send(ctx context.Context, notification messageSender.Notification, config map[string]any) error {
	raw, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	result := make(chan error, 1)
	var once sync.Once
	finish := func(err error) { once.Do(func() { result <- err }) }
	if !c.host.RunOnLoop(func(vm *goja.Runtime) {
		err := c.host.RunJob(vm, "plugin notification "+c.short, func() error {
			value, err := c.handler(goja.Undefined(), vm.ToValue(payload), vm.ToValue(config))
			if err != nil {
				finish(err)
				return nil
			}
			if _, ok := value.Export().(*goja.Promise); !ok {
				finish(nil)
				return nil
			}
			then, ok := goja.AssertFunction(value.ToObject(vm).Get("then"))
			if !ok {
				finish(fmt.Errorf("notification promise has no then method"))
				return nil
			}
			_, err = then(value, vm.ToValue(func(goja.FunctionCall) goja.Value { finish(nil); return goja.Undefined() }), vm.ToValue(func(call goja.FunctionCall) goja.Value {
				finish(fmt.Errorf("notification channel rejected: %s", call.Argument(0).String()))
				return goja.Undefined()
			}))
			if err != nil {
				finish(err)
			}
			return nil
		})
		if err != nil {
			finish(err)
		}
	}) {
		return fmt.Errorf("plugin %q notification runtime is closed", c.short)
	}
	timer := time.NewTimer(c.host.Timeout())
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("plugin %q notification timed out", c.short)
	}
}
