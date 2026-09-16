package server

import (
	"context"
	"fmt"

	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/utils/geoip"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/Aone2233/nekomari/web/oauth"
)

// InitProviders initializes providers needed by the normal application.
func (a *App) InitProviders() error {
	a.initOAuth()

	go geoip.InitGeoIp()
	a.addCleanup("geoip", func(context.Context) error { return geoip.Shutdown() })

	messageSender.Initialize()
	a.addCleanup("message-sender", func(context.Context) error { return messageSender.Shutdown() })
	return nil
}

// initOAuth initializes OAuth once. Restricted authenticated guides need it
// before the normal provider initialization phase.
func (a *App) initOAuth() {
	if a.oauthReady {
		return
	}
	if err := oauth.Initialize(); err != nil {
		logger.Errorf("server", "Failed to initialize OAuth provider: %v", err)
		auditlog.EventLog("error", fmt.Sprintf("Failed to initialize OAuth provider: %v", err))
	}
	a.oauthReady = true
	a.addCleanup("oauth", func(context.Context) error { return oauth.Shutdown() })
}
