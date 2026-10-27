package server

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/internal/config"
)

func TestGeoIPReloadHandlerWatchesEnableAndProvider(t *testing.T) {
	for _, test := range []struct {
		key     string
		old     any
		new     any
		wantRun bool
	}{
		{config.GeoIpEnabledKey, true, false, true},
		{config.GeoIpEnabledKey, false, true, true},
		{config.GeoIpProviderKey, "ipinfo", "geojs", true},
		{config.GeoIpEnabledKey, true, true, false},
		{config.ThemeKey, "default", "other", false},
	} {
		t.Run(test.key, func(t *testing.T) {
			called := make(chan struct{}, 1)
			reloadGeoIP(config.ConfigEvent{
				Old: map[string]any{test.key: test.old},
				New: map[string]any{test.key: test.new},
			}, func() { called <- struct{}{} })
			if !test.wantRun {
				select {
				case <-called:
					t.Fatal("unrelated or unchanged setting triggered GeoIP reload")
				case <-time.After(20 * time.Millisecond):
				}
				return
			}
			select {
			case <-called:
			case <-time.After(5 * time.Second):
				t.Fatal("GeoIP setting change did not trigger reload")
			}
		})
	}
}
