package clients

import (
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
)

// The caller must own the map it gets back. Returning the cached map let one
// caller's write change what every later caller saw until the next client write.
func TestHiddenClientsReturnsOwnedSnapshot(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:clients-visibility?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if err := db.Create(&models.Client{UUID: "visible-node", Name: "visible", Token: "test-only-token"}).Error; err != nil {
		t.Fatal(err)
	}

	first, err := HiddenClients()
	if err != nil {
		t.Fatal(err)
	}
	if first["visible-node"] {
		t.Fatal("new client reported hidden")
	}
	first["visible-node"] = true
	first["injected"] = true

	second, err := HiddenClients()
	if err != nil {
		t.Fatal(err)
	}
	if second["visible-node"] {
		t.Fatal("caller mutation leaked into the cache")
	}
	if second["injected"] {
		t.Fatal("caller-added entry leaked into the cache")
	}
}
