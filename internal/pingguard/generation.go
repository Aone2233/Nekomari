package pingguard

import (
	"crypto/rand"
	"encoding/hex"
)

// NewGeneration creates a fresh incarnation for a pinned task mutation.
// The caller must persist it atomically with the change under the task guard.
func NewGeneration() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidGeneration reports whether a persisted incarnation has the same
// canonical representation as generations issued by NewGeneration.
func ValidGeneration(generation string) bool {
	if len(generation) != 32 {
		return false
	}
	for _, c := range generation {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
