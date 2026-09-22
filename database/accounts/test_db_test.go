package accounts

import (
	"os"
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/internal/dbcache"
)

func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:accounts_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	dbcache.Watch(db)
	code := m.Run()
	_ = dbcore.Close()
	os.Exit(code)
}

func createTestAccount(t *testing.T, username string) string {
	t.Helper()
	user, err := CreateAccount(username, "test-only-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := DeleteAccountByUsername(username); err != nil {
			t.Error(err)
		}
	})
	return user.UUID
}
