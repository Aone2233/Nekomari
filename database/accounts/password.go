package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/crypto/argon2"
	"strings"
	"time"
)

const passwordPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

const (
	// Each Argon2id evaluation holds 19456 KiB, so admission is bounded to keep
	// concurrent logins from multiplying that by the request count.
	passwordWorkersCapacity = 2
	passwordMaxBytes        = 4096
)

// passwordAdmissionWait is how long a check waits for a KDF slot before reporting
// ErrPasswordBusy. Waiting beats rejecting: the bound used to be an immediate
// refusal, and the refusal was reported as a failed password check, so with two
// evaluations already running every further caller — including the administrator
// with the correct password — was told "Invalid credentials". It is a variable
// only so tests can shorten it.
var passwordAdmissionWait = 5 * time.Second

// ErrPasswordBusy reports that the KDF admission queue stayed full for the whole
// wait window. It is deliberately distinct from "wrong password" so callers can
// answer "try again" instead of consuming a login attempt.
var ErrPasswordBusy = errors.New("password service is busy")

// PasswordRetryAfter is how long a caller should wait before retrying a check that
// returned ErrPasswordBusy.
func PasswordRetryAfter() time.Duration { return passwordAdmissionWait }

var passwordWorkers = make(chan struct{}, passwordWorkersCapacity)

// acquirePasswordWorker reserves one KDF slot, waiting up to wait for one to free.
func acquirePasswordWorker(wait time.Duration) (func(), error) {
	select {
	case passwordWorkers <- struct{}{}:
		return func() { <-passwordWorkers }, nil
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case passwordWorkers <- struct{}{}:
		return func() { <-passwordWorkers }, nil
	case <-timer.C:
		return nil, ErrPasswordBusy
	}
}

func hashPassword(password string) (string, error) {
	if len(password) > passwordMaxBytes {
		return "", fmt.Errorf("password exceeds %d bytes", passwordMaxBytes)
	}
	release, err := acquirePasswordWorker(passwordAdmissionWait)
	if err != nil {
		return "", err
	}
	defer release()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}

// verifyPassword reports whether password matches stored, whether stored still
// holds the legacy format, and whether the check could not run at all because the
// KDF admission queue was saturated. A busy check is never reported as a wrong
// password.
func verifyPassword(stored, password string) (ok, legacy bool, err error) {
	if len(password) > passwordMaxBytes {
		return false, false, nil
	}
	if !strings.HasPrefix(stored, "$") {
		sum := sha256.Sum256([]byte(password + "06Wm4Jv1Hkxx"))
		encoded := base64.StdEncoding.EncodeToString(sum[:])
		return subtle.ConstantTimeCompare([]byte(encoded), []byte(stored)) == 1, true, nil
	}
	if !strings.HasPrefix(stored, passwordPrefix) {
		return false, false, nil
	}
	parts := strings.Split(strings.TrimPrefix(stored, passwordPrefix), "$")
	if len(parts) != 2 {
		return false, false, nil
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil || len(salt) != 16 {
		return false, false, nil
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(want) != 32 {
		return false, false, nil
	}
	release, err := acquirePasswordWorker(passwordAdmissionWait)
	if err != nil {
		return false, false, err
	}
	defer release()
	got := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(got, want) == 1, false, nil
}
