package public

import (
	"os"
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
)

func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:web_api_public_test?mode=memory&cache=shared"

	db := dbcore.GetDBInstance()
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}

	code := m.Run()
	_ = dbcore.Close()
	os.Exit(code)
}

// Tests using the production entry points must not inherit other tests' buckets.
// These tests are deliberately serial because the entry points share this state.
func isolateLoginLimiter(t *testing.T) {
	t.Helper()
	previous := defaultLoginLimiter
	defaultLoginLimiter = newLoginLimiter()
	t.Cleanup(func() { defaultLoginLimiter = previous })
}
