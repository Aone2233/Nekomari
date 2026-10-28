package geoip

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patrickmn/go-cache"
)

type countryCacheProvider struct {
	name  string
	info  *GeoInfo
	err   error
	calls int
}

func (p *countryCacheProvider) Name() string { return p.name }
func (p *countryCacheProvider) GetGeoInfo(net.IP) (*GeoInfo, error) {
	p.calls++
	return p.info, p.err
}
func (p *countryCacheProvider) UpdateDatabase() error { return nil }
func (p *countryCacheProvider) Close() error          { return nil }

func withCountryCacheProvider(t *testing.T, provider GeoIPService) {
	t.Helper()
	old := CurrentProvider
	oldCache := geoCache
	CurrentProvider = provider
	geoCache.Flush()
	t.Cleanup(func() { CurrentProvider = old; geoCache = oldCache; geoCache.Flush() })
}

func assertCountryCacheExpiry(t *testing.T, key string, ttl time.Duration) {
	t.Helper()
	_, expiry, ok := geoCache.GetWithExpiration(key)
	if !ok {
		t.Fatalf("cache entry %q missing", key)
	}
	remaining := time.Until(expiry)
	if remaining <= ttl-time.Second || remaining > ttl {
		t.Fatalf("cache TTL = %s, want approximately %s", remaining, ttl)
	}
}

func TestGeoIPEmptyCountryCacheTTLAndRecovery(t *testing.T) {
	for _, test := range []struct {
		name string
		info *GeoInfo
		ttl  time.Duration
	}{
		{"empty", &GeoInfo{}, emptyCountryCacheTTL},
		{"whitespace", &GeoInfo{ISOCode: " ", Name: "\t"}, emptyCountryCacheTTL},
		{"country code", &GeoInfo{ISOCode: "US"}, 48 * time.Hour},
		{"country name", &GeoInfo{Name: "United States"}, 48 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &countryCacheProvider{name: t.Name(), info: test.info}
			withCountryCacheProvider(t, provider)
			ip := net.ParseIP("192.0.2.1")
			key := provider.Name() + ":" + ip.String()
			if info, err := GetGeoInfo(ip); err != nil || info != test.info {
				t.Fatalf("first lookup = %#v, %v", info, err)
			}
			assertCountryCacheExpiry(t, key, test.ttl)
			provider.info = &GeoInfo{ISOCode: "SG", Name: "Singapore"}
			if info, err := GetGeoInfo(ip); err != nil || info != test.info || provider.calls != 1 {
				t.Fatalf("cached lookup = %#v, %v, provider calls %d", info, err, provider.calls)
			}
			// Model an expired entry without relying on clock precision or sleeps.
			geoCache = cache.NewFrom(48*time.Hour, 0, map[string]cache.Item{
				key: {Object: test.info, Expiration: time.Now().Add(-time.Minute).UnixNano()},
			})
			if info, err := GetGeoInfo(ip); err != nil || info == nil || info.ISOCode != "SG" || provider.calls != 2 {
				t.Fatalf("lookup after expiry = %#v, %v, provider calls %d", info, err, provider.calls)
			}
			assertCountryCacheExpiry(t, key, 48*time.Hour)
		})
	}
}

func TestGeoIPErrorsAndNilResultsAreNotCached(t *testing.T) {
	for _, test := range []struct {
		name string
		info *GeoInfo
		err  error
	}{
		{"error", nil, errors.New("upstream failure")},
		{"partial result with error", &GeoInfo{ISOCode: "US"}, errors.New("upstream failure")},
		{"nil result", nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &countryCacheProvider{name: t.Name(), info: test.info, err: test.err}
			withCountryCacheProvider(t, provider)
			ip := net.ParseIP("192.0.2.2")
			for range 2 {
				info, err := GetGeoInfo(ip)
				if info != test.info || err != test.err {
					t.Fatalf("lookup = %#v, %v", info, err)
				}
			}
			if provider.calls != 2 {
				t.Fatalf("provider calls = %d, want 2", provider.calls)
			}
			if _, found := geoCache.Get(provider.Name() + ":" + ip.String()); found {
				t.Fatal("failed or nil lookup must not be cached")
			}
		})
	}
}

type countryCacheRoundTripper func(*http.Request) (*http.Response, error)

func (f countryCacheRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGeoIPIPInfoEmptyResponseRetriesAfterExpiry(t *testing.T) {
	calls := 0
	provider := &IPInfoService{Client: &http.Client{Transport: countryCacheRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "ipinfo.io" || req.URL.Path != "/192.0.2.3/json" {
			t.Fatalf("unexpected upstream URL: %s", req.URL)
		}
		calls++
		body := `{}`
		if calls > 1 {
			body = `{"country":"US"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}, nil
	})}}
	withCountryCacheProvider(t, provider)
	ip := net.ParseIP("192.0.2.3")
	key := provider.Name() + ":" + ip.String()
	info, err := GetGeoInfo(ip)
	if err != nil || info == nil || info.ISOCode != "" || info.Name != "" {
		t.Fatalf("empty ipinfo response = %#v, %v", info, err)
	}
	assertCountryCacheExpiry(t, key, emptyCountryCacheTTL)
	info, err = GetGeoInfo(ip)
	if err != nil || info == nil || info.ISOCode != "" || calls != 1 {
		t.Fatalf("cached ipinfo response = %#v, %v, upstream calls %d", info, err, calls)
	}
	geoCache = cache.NewFrom(48*time.Hour, 0, map[string]cache.Item{
		key: {Object: info, Expiration: time.Now().Add(-time.Minute).UnixNano()},
	})
	info, err = GetGeoInfo(ip)
	if err != nil || info == nil || info.ISOCode != "US" || info.Name != "US" || calls != 2 {
		t.Fatalf("recovered ipinfo response = %#v, %v, upstream calls %d", info, err, calls)
	}
	assertCountryCacheExpiry(t, key, 48*time.Hour)
}

func TestGeoIPProviderReloadClearsEmptyCountryCache(t *testing.T) {
	old := &countryCacheProvider{name: "same-name", info: &GeoInfo{}}
	withCountryCacheProvider(t, old)
	ip := net.ParseIP("192.0.2.4")
	if _, err := GetGeoInfo(ip); err != nil {
		t.Fatal(err)
	}
	newProvider := &countryCacheProvider{name: old.name, info: &GeoInfo{ISOCode: "SG"}}
	reloadMu.Lock()
	err := replaceProvider(newProvider)
	reloadMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	info, err := GetGeoInfo(ip)
	if err != nil || info == nil || info.ISOCode != "SG" || newProvider.calls != 1 {
		t.Fatalf("lookup after reload = %#v, %v, provider calls %d", info, err, newProvider.calls)
	}
}

type overlappingCountryProvider struct {
	calls        atomic.Int64
	emptyStarted chan struct{}
	finishEmpty  chan struct{}
}

func (p *overlappingCountryProvider) Name() string { return "overlapping-country-test" }
func (p *overlappingCountryProvider) GetGeoInfo(net.IP) (*GeoInfo, error) {
	if p.calls.Add(1) == 1 {
		close(p.emptyStarted)
		<-p.finishEmpty
		return &GeoInfo{}, nil
	}
	return &GeoInfo{ISOCode: "US"}, nil
}
func (p *overlappingCountryProvider) UpdateDatabase() error { return nil }
func (p *overlappingCountryProvider) Close() error          { return nil }

func TestGeoIPLateEmptyLookupDoesNotReplacePositiveCache(t *testing.T) {
	provider := &overlappingCountryProvider{emptyStarted: make(chan struct{}), finishEmpty: make(chan struct{})}
	withCountryCacheProvider(t, provider)
	ip := net.ParseIP("192.0.2.5")
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = GetGeoInfo(ip)
	}()
	<-provider.emptyStarted
	info, err := GetGeoInfo(ip)
	close(provider.finishEmpty)
	<-finished
	if err != nil || info == nil || info.ISOCode != "US" {
		t.Fatalf("positive lookup = %#v, %v", info, err)
	}
	info, err = GetGeoInfo(ip)
	if err != nil || info == nil || info.ISOCode != "US" || provider.calls.Load() != 2 {
		t.Fatalf("cached lookup = %#v, %v, provider calls %d", info, err, provider.calls.Load())
	}
	assertCountryCacheExpiry(t, provider.Name()+":"+ip.String(), 48*time.Hour)
}

type overlappingEmptyProvider struct {
	calls      atomic.Int64
	started    chan struct{}
	finishSlow chan struct{}
}

func (p *overlappingEmptyProvider) Name() string { return "overlapping-empty-test" }
func (p *overlappingEmptyProvider) GetGeoInfo(net.IP) (*GeoInfo, error) {
	if p.calls.Add(1) == 1 {
		close(p.started)
		<-p.finishSlow
	}
	return &GeoInfo{}, nil
}
func (p *overlappingEmptyProvider) UpdateDatabase() error { return nil }
func (p *overlappingEmptyProvider) Close() error          { return nil }

func TestGeoIPLateEmptyLookupDoesNotExtendEmptyExpiry(t *testing.T) {
	provider := &overlappingEmptyProvider{started: make(chan struct{}), finishSlow: make(chan struct{})}
	withCountryCacheProvider(t, provider)
	ip := net.ParseIP("192.0.2.6")
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = GetGeoInfo(ip)
	}()
	<-provider.started
	_, err := GetGeoInfo(ip)
	if err != nil {
		close(provider.finishSlow)
		<-finished
		t.Fatal(err)
	}
	key := provider.Name() + ":" + ip.String()
	_, expiryBefore, found := geoCache.GetWithExpiration(key)
	close(provider.finishSlow)
	<-finished
	_, expiryAfter, stillFound := geoCache.GetWithExpiration(key)
	if !found || !stillFound || !expiryAfter.Equal(expiryBefore) || provider.calls.Load() != 2 {
		t.Fatalf("expiry before/after = %v/%v, calls = %d", expiryBefore, expiryAfter, provider.calls.Load())
	}
}
