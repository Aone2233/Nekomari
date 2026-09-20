package accounts

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashAndLegacyVerification(t *testing.T) {
	const password = "test-password-only"
	first, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, passwordPrefix) {
		t.Fatal("passwords must use random salts and Argon2id")
	}
	if ok, legacy, err := verifyPassword(first, password); err != nil || !ok || legacy {
		t.Fatalf("new hash rejected: ok=%v legacy=%v err=%v", ok, legacy, err)
	}
	if ok, _, err := verifyPassword(first, "wrong"); err != nil || ok {
		t.Fatal("wrong password accepted")
	}
	sum := sha256.Sum256([]byte(password + "06Wm4Jv1Hkxx"))
	if ok, legacy, err := verifyPassword(base64.StdEncoding.EncodeToString(sum[:]), password); err != nil || !ok || !legacy {
		t.Fatal("legacy migration input rejected")
	}
	for _, stored := range []string{"$argon2id$v=19$m=999999999,t=2,p=1$bad$bad", passwordPrefix + "bad$bad"} {
		if ok, _, err := verifyPassword(stored, password); err != nil || ok {
			t.Fatal("malformed hash accepted")
		}
	}
	if _, err := hashPassword(strings.Repeat("a", passwordMaxBytes+1)); err == nil {
		t.Fatal("oversized password accepted")
	}
}

// holdPasswordWorkers takes every KDF slot and returns a release for each.
func holdPasswordWorkers(t *testing.T) []func() {
	t.Helper()
	releases := make([]func(), 0, passwordWorkersCapacity)
	for i := 0; i < passwordWorkersCapacity; i++ {
		release, err := acquirePasswordWorker(time.Second)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	return releases
}

// A saturated pool delays a check and then reports busy. It must not report the
// password as wrong: that is the defect that told concurrent callers, including
// the administrator with the correct password, that their credentials failed.
func TestSaturatedAdmissionReportsBusyNotMismatch(t *testing.T) {
	const password = "test-password-only"
	hashed, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	original := passwordAdmissionWait
	passwordAdmissionWait = 50 * time.Millisecond
	defer func() { passwordAdmissionWait = original }()

	releases := holdPasswordWorkers(t)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	if _, err := hashPassword(password); !errors.Is(err, ErrPasswordBusy) {
		t.Fatalf("saturated hashPassword returned %v, want ErrPasswordBusy", err)
	}
	ok, legacy, err := verifyPassword(hashed, password)
	if !errors.Is(err, ErrPasswordBusy) {
		t.Fatalf("saturated verifyPassword returned %v, want ErrPasswordBusy", err)
	}
	if ok || legacy {
		t.Fatalf("busy check claimed ok=%v legacy=%v", ok, legacy)
	}

	// Freeing one slot must admit the next check rather than fail it.
	releases[0]()
	releases = releases[1:]
	if ok, _, err := verifyPassword(hashed, password); err != nil || !ok {
		t.Fatalf("correct password rejected after a slot freed: ok=%v err=%v", ok, err)
	}
}
