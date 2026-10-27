package clients

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"gorm.io/gorm"
)

func TestRotateClientTokenCommitsBeforeReset(t *testing.T) {
	const id = "token-rotation-commit"
	newTargetVersionClient(t, id)
	resets := 0
	token, err := RotateClientTokenAndReset(t.Context(), id, func() {
		resets++
		if _, err := GetClientUUIDByToken("token-" + id); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("old token still resolves during reset: %v", err)
		}
	})
	if err != nil || resets != 1 || !strings.HasPrefix(token, "na_") {
		t.Fatalf("rotation failed: %v, resets %d", err, resets)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "na_"))
	if err != nil || len(decoded) != 32 {
		t.Fatal("replacement does not carry 256 random bits")
	}
	if got, err := GetClientUUIDByToken(token); err != nil || got != id {
		t.Fatalf("new token cannot authenticate: %v", err)
	}
	second, err := RotateClientTokenAndReset(t.Context(), id, nil)
	if err != nil || second == token {
		t.Fatalf("second rotation did not replace token: %v", err)
	}
	if _, err := GetClientUUIDByToken(token); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("intermediate token still resolves")
	}
}

func TestRotateClientTokenFailureDoesNotResetOrExposeReplacement(t *testing.T) {
	const id = "token-rotation-failure"
	newTargetVersionClient(t, id)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, call := range map[string]func() (string, error){
		"canceled": func() (string, error) {
			return RotateClientTokenAndReset(ctx, id, func() { t.Error("canceled operation reset client") })
		},
		"missing": func() (string, error) {
			return RotateClientTokenAndReset(t.Context(), "token-rotation-missing", func() { t.Error("missing client was reset") })
		},
	} {
		t.Run(name, func(t *testing.T) {
			if token, err := call(); err == nil || token != "" {
				t.Fatalf("failed rotation returned a replacement: %v", err)
			}
		})
	}
	db := dbcore.GetDBInstance()
	if err := db.Exec(`CREATE TRIGGER refuse_token_rotation BEFORE UPDATE OF token ON clients
		WHEN OLD.uuid = 'token-rotation-failure' BEGIN SELECT RAISE(ABORT, 'refuse rotation'); END`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec("DROP TRIGGER refuse_token_rotation") })
	if token, err := RotateClientTokenAndReset(t.Context(), id, func() { t.Error("failed write reset client") }); err == nil || token != "" {
		t.Fatal("failed DB update returned a replacement")
	}
	if got, err := GetClientUUIDByToken("token-" + id); err != nil || got != id {
		t.Fatalf("failed rotation changed old credential: %v", err)
	}
}

func TestClientMapsCannotBypassCredentialRotation(t *testing.T) {
	const id = "token-rotation-map-policy"
	newTargetVersionClient(t, id)
	db := dbcore.GetDBInstance()
	for _, field := range []string{"token", "Token", "TOKEN", "`token`", `"token"`, "[token]"} {
		if err := SaveClient(map[string]any{"uuid": id, field: "unreviewed-token"}); err == nil {
			t.Errorf("generic metadata edit accepted credential field %s", field)
		}
	}
	if err := SaveClientInfo(map[string]any{"uuid": id, "token": "forged", "Token": "also-forged",
		"name": "forged-name", "target_version": "forged-target", "hidden": true, "cpu_name": "reported CPU", "version": "v1.6.24"}); err != nil {
		t.Fatal(err)
	}
	var row models.Client
	if err := db.Where("uuid = ?", id).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Token != "token-"+id || row.Name != id || row.TargetVersion != "" || row.Hidden || row.CpuName != "reported CPU" || row.Version != "v1.6.24" {
		t.Fatal("basic-info map changed policy/credentials or dropped supported host facts")
	}
}
