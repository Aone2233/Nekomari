package netstatic

import (
	"errors"
	"testing"
)

// setProvider installs a provider for one test and removes it afterwards.
func setProvider(t *testing.T, provider func() ([]string, error)) {
	t.Helper()
	SetNICProvider(provider)
	t.Cleanup(func() { SetNICProvider(nil) })
}

func currentNics() []string {
	mu.RLock()
	defer mu.RUnlock()
	return append([]string(nil), config.Nics...)
}

// The whitelist used to be a startup snapshot: an interface that appeared later
// (a docker bridge, a hot-plugged NIC) was never counted, silently. The refresh
// has to change the whitelist, and the sampling path has to be the thing that
// asks.
func TestNICProviderRefreshesTheWhitelistBeforeEachSample(t *testing.T) {
	resetState(t)
	calls := 0
	nics := []string{"eth0"}
	setProvider(t, func() ([]string, error) {
		calls++
		return append([]string(nil), nics...), nil
	})

	mu.Lock()
	refreshNicsLocked()
	mu.Unlock()
	if got := currentNics(); len(got) != 1 || got[0] != "eth0" {
		t.Fatalf("whitelist = %v, want [eth0]", got)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}

	// A bridge came up since the snapshot; the next sample must learn about it.
	nics = []string{"eth0", "br0"}
	mu.Lock()
	sampleOnceLocked()
	mu.Unlock()
	if calls < 2 {
		t.Fatalf("provider calls = %d, want the sampling path to refresh", calls)
	}
	got := currentNics()
	if len(got) != 2 || got[0] != "eth0" || got[1] != "br0" {
		t.Fatalf("whitelist after sample = %v, want [eth0 br0]", got)
	}
	mu.RLock()
	stored := append([]string(nil), store.Config.Nics...)
	mu.RUnlock()
	if len(stored) != 2 || stored[1] != "br0" {
		t.Fatalf("stored config = %v, want it to follow the provider", stored)
	}
}

// The provider runs every sampling interval; an unchanged list must not keep
// marking the ledger dirty, or every sample would schedule a full rewrite.
func TestNICProviderOnlyWritesWhenTheListChanges(t *testing.T) {
	resetState(t)
	// The provider reads this variable, so the test can change the answer without
	// calling SetNICProvider (which takes the same lock the refresh runs under).
	current := []string{"eth0"}
	setProvider(t, func() ([]string, error) { return append([]string(nil), current...), nil })

	mu.Lock()
	refreshNicsLocked()
	storeDirty = false
	refreshNicsLocked()
	dirty := storeDirty
	mu.Unlock()
	if dirty {
		t.Fatal("an unchanged interface list marked the ledger dirty")
	}

	// Reordering the same set is not a change either.
	current = []string{"eth1", "eth0"}
	mu.Lock()
	refreshNicsLocked()
	storeDirty = false
	mu.Unlock()

	current = []string{"eth0", "eth1"}
	mu.Lock()
	refreshNicsLocked()
	dirty = storeDirty
	mu.Unlock()
	if dirty {
		t.Fatal("reordering the same interfaces marked the ledger dirty")
	}
}

// A provider failure keeps the previous whitelist and logs once, so a bad
// enumeration does not turn into a log line every sampling interval.
func TestNICProviderFailureKeepsThePreviousList(t *testing.T) {
	resetState(t)
	mu.Lock()
	config.Nics = []string{"eth0"}
	store.Config.Nics = []string{"eth0"}
	mu.Unlock()

	setProvider(t, func() ([]string, error) { return nil, errors.New("no interfaces") })
	mu.Lock()
	refreshNicsLocked()
	firstLogged := nicProviderFailureLogged
	refreshNicsLocked()
	secondLogged := nicProviderFailureLogged
	mu.Unlock()

	if got := currentNics(); len(got) != 1 || got[0] != "eth0" {
		t.Fatalf("whitelist after a provider failure = %v, want the previous [eth0]", got)
	}
	if !firstLogged || !secondLogged {
		t.Fatalf("failure logging state = %v/%v, want it logged once and the flag kept", firstLogged, secondLogged)
	}

	// Recovery resets the suppression, so the next failure is reported again.
	setProvider(t, func() ([]string, error) { return []string{"eth0", "br0"}, nil })
	mu.Lock()
	refreshNicsLocked()
	recovered := nicProviderFailureLogged
	mu.Unlock()
	if recovered {
		t.Fatal("a successful refresh must reset the failure suppression")
	}
}

// An empty answer means the caller's filter matched nothing. Turning that into
// "monitor every interface" would silently start counting virtual interfaces.
func TestNICProviderEmptyAnswerKeepsThePreviousList(t *testing.T) {
	resetState(t)
	mu.Lock()
	config.Nics = []string{"eth0"}
	store.Config.Nics = []string{"eth0"}
	mu.Unlock()

	setProvider(t, func() ([]string, error) { return []string{}, nil })
	mu.Lock()
	refreshNicsLocked()
	mu.Unlock()
	if got := currentNics(); len(got) != 1 || got[0] != "eth0" {
		t.Fatalf("whitelist after an empty answer = %v, want the previous [eth0]", got)
	}
}

// The refresh must drop baselines and buckets for interfaces it stops monitoring,
// otherwise a NIC that reappears later is measured against a stale baseline.
func TestRefreshPrunesInterfacesThatLeftTheWhitelist(t *testing.T) {
	resetState(t)
	mu.Lock()
	lastCounters["eth0"] = CounterSample{Tx: 10, Rx: 10, At: 1}
	lastCounters["eth1"] = CounterSample{Tx: 20, Rx: 20, At: 1}
	staticCache["eth0"] = []TrafficData{{Timestamp: 1, Tx: 1, Rx: 1}}
	staticCache["eth1"] = []TrafficData{{Timestamp: 1, Tx: 2, Rx: 2}}
	mu.Unlock()

	setProvider(t, func() ([]string, error) { return []string{"eth0"}, nil })
	mu.Lock()
	refreshNicsLocked()
	_, keptBaseline := lastCounters["eth0"]
	_, droppedBaseline := lastCounters["eth1"]
	_, keptCache := staticCache["eth0"]
	_, droppedCache := staticCache["eth1"]
	mu.Unlock()

	if !keptBaseline || !keptCache {
		t.Fatal("a still-monitored interface lost its baseline or cache")
	}
	if droppedBaseline || droppedCache {
		t.Fatal("an interface that left the whitelist kept its baseline or cache")
	}
}
