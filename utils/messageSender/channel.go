package messageSender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/Aone2233/nekomari/database"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/managedconfig"
	"gorm.io/gorm"
)

// Notification and NotificationChannel mirror Komari's plugin channel contract.
type Notification struct {
	Event   models.EventMessage `json:"event"`
	Title   string              `json:"title"`
	Message string              `json:"message"`
}
type NotificationChannel interface {
	Send(context.Context, Notification, map[string]any) error
}
type NotificationChannelInfo struct {
	ID            string               `json:"id"`
	Configuration models.Configuration `json:"configuration"`
}
type channelEntry struct {
	configuration models.Configuration
	channel       NotificationChannel
	selectionID   string
}

var channels = struct {
	sync.RWMutex
	entries map[string]*channelEntry
}{entries: make(map[string]*channelEntry)}

func RegisterNotificationChannel(id string, selectionID string, configuration models.Configuration, channel NotificationChannel) error {
	id = strings.TrimSpace(id)
	if id == "" || selectionID == "" || channel == nil {
		return errors.New("notification channel id and handler are required")
	}
	if !reflect.TypeOf(channel).Comparable() || (reflect.ValueOf(channel).Kind() == reflect.Ptr && reflect.ValueOf(channel).IsNil()) {
		return errors.New("notification channel handler must be non-nil and comparable")
	}
	if configuration.Type == "" {
		configuration.Type = models.ThemeConfigurationManaged
	}
	if configuration.Type != models.ThemeConfigurationManaged {
		return errors.New("notification channel configuration must be managed")
	}
	channels.Lock()
	defer channels.Unlock()
	if _, exists := channels.entries[id]; exists {
		return fmt.Errorf("notification channel %q is already registered", id)
	}
	channels.entries[id] = &channelEntry{configuration, channel, selectionID}
	return nil
}

// UnregisterNotificationChannel removes only the handler that registered the ID.
func UnregisterNotificationChannel(id string, channel NotificationChannel) {
	if channel == nil || !reflect.TypeOf(channel).Comparable() {
		return
	}
	channels.Lock()
	if entry := channels.entries[id]; entry != nil && entry.channel == channel {
		delete(channels.entries, id)
	}
	channels.Unlock()
}
func NotificationChannelRegistered(id string) bool {
	channels.RLock()
	defer channels.RUnlock()
	return channels.entries[id] != nil
}
func ListNotificationChannels() []NotificationChannelInfo {
	channels.RLock()
	defer channels.RUnlock()
	result := make([]NotificationChannelInfo, 0, len(channels.entries))
	for id, e := range channels.entries {
		result = append(result, NotificationChannelInfo{id, e.configuration})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func GetNotificationChannelConfiguration(id string) (models.Configuration, map[string]any, bool, error) {
	channels.RLock()
	e := channels.entries[id]
	channels.RUnlock()
	if e == nil {
		return models.Configuration{}, nil, false, nil
	}
	values, err := channelValues(e.selectionID, e.configuration)
	return e.configuration, values, true, err
}
func channelValues(id string, cfg models.Configuration) (map[string]any, error) {
	values := map[string]any{}
	saved, err := database.GetMessageSenderConfigByName(id)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(saved.Addition), &values); err != nil {
			return nil, err
		}
		if values == nil {
			values = map[string]any{}
		}
	}
	items := managedconfig.Items(cfg)
	for _, item := range items {
		if item.Key != "" {
			if _, ok := values[item.Key]; !ok {
				values[item.Key] = managedconfig.DefaultValue(item)
			}
		}
	}
	if err = managedconfig.ResolveForOutput(values, items); err != nil {
		return nil, err
	}
	return values, nil
}
func CallNotificationChannel(ctx context.Context, id string, notification Notification) (bool, error) {
	channels.RLock()
	var e *channelEntry
	for _, candidate := range channels.entries {
		if candidate.selectionID == id {
			e = candidate
			break
		}
	}
	channels.RUnlock()
	if e == nil {
		return false, nil
	}
	values, err := channelValues(e.selectionID, e.configuration)
	if err != nil {
		return true, err
	}
	if err := e.channel.Send(ctx, notification, values); err != nil {
		// Third-party handlers can include credentials in thrown errors.
		// Do not put their text into logs or API responses.
		return true, errors.New("plugin notification delivery failed")
	}
	return true, nil
}

func ChannelSelectionID(id string) (string, bool) {
	channels.RLock()
	defer channels.RUnlock()
	e := channels.entries[id]
	if e == nil {
		return "", false
	}
	return e.selectionID, true
}

func PluginChannelChoices() map[string]models.Configuration {
	channels.RLock()
	defer channels.RUnlock()
	result := make(map[string]models.Configuration, len(channels.entries))
	for _, e := range channels.entries {
		result[e.selectionID] = e.configuration
	}
	return result
}
