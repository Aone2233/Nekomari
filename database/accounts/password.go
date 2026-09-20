package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"golang.org/x/crypto/argon2"
	"strings"
)

const passwordPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

var passwordWorkers = make(chan struct{}, 2)

func hashPassword(password string) (string, error) {
	if len(password) > 4096 {
		return "", fmt.Errorf("password exceeds 4096 bytes")
	}
	select {
	case passwordWorkers <- struct{}{}:
	default:
		return "", fmt.Errorf("password service is busy")
	}
	defer func() { <-passwordWorkers }()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}

func verifyPassword(stored, password string) (ok, legacy bool) {
	if len(password) > 4096 {
		return false, false
	}
	if !strings.HasPrefix(stored, "$") {
		sum := sha256.Sum256([]byte(password + "06Wm4Jv1Hkxx"))
		encoded := base64.StdEncoding.EncodeToString(sum[:])
		return subtle.ConstantTimeCompare([]byte(encoded), []byte(stored)) == 1, true
	}
	if !strings.HasPrefix(stored, passwordPrefix) {
		return false, false
	}
	parts := strings.Split(strings.TrimPrefix(stored, passwordPrefix), "$")
	if len(parts) != 2 {
		return false, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil || len(salt) != 16 {
		return false, false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(want) != 32 {
		return false, false
	}
	select {
	case passwordWorkers <- struct{}{}:
	default:
		return false, false
	}
	defer func() { <-passwordWorkers }()
	got := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(got, want) == 1, false
}
