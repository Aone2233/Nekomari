package messageSender

import (
	"errors"

	"github.com/Aone2233/nekomari/internal/config"
)

// SendOperationalNotice is a single attempt with a stable provider lifetime.
// Disabled/unconfigured delivery is not an acknowledgement. Never propagate a
// provider error containing credentials to an operational log or alert state.
func SendOperationalNotice(message, title string) error {
	enabled, err := config.GetAs[bool](config.NotificationEnabledKey)
	if err != nil || !enabled {
		return errors.New("operational notifications unavailable")
	}
	mu.Lock()
	defer mu.Unlock()
	if currentProvider == nil || currentProvider.GetName() == "empty" {
		return errors.New("operational notifications unavailable")
	}
	if err := currentProvider.SendTextMessage(message, title); err != nil {
		return errors.New("operational notification delivery failed")
	}
	return nil
}
