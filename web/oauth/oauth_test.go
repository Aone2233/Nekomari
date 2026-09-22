package oauth

import "testing"

func TestFailedReloadPreservesProvider(t *testing.T) {
	defer Shutdown()
	if err := LoadProvider("github", "{}"); err != nil {
		t.Fatal(err)
	}
	previous := CurrentProvider()
	for _, tc := range []struct{ name, config string }{{"unknown", "{}"}, {"github", "{"}} {
		if err := LoadProvider(tc.name, tc.config); err == nil {
			t.Fatal("invalid reload succeeded")
		}
		if CurrentProvider() != previous {
			t.Fatal("working provider replaced")
		}
	}
}
