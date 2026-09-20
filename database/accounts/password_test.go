package accounts

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
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
	if ok, legacy := verifyPassword(first, password); !ok || legacy {
		t.Fatal("new hash rejected")
	}
	if ok, _ := verifyPassword(first, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	sum := sha256.Sum256([]byte(password + "06Wm4Jv1Hkxx"))
	if ok, legacy := verifyPassword(base64.StdEncoding.EncodeToString(sum[:]), password); !ok || !legacy {
		t.Fatal("legacy migration input rejected")
	}
	for _, stored := range []string{"$argon2id$v=19$m=999999999,t=2,p=1$bad$bad", passwordPrefix + "bad$bad"} {
		if ok, _ := verifyPassword(stored, password); ok {
			t.Fatal("malformed hash accepted")
		}
	}
	if _, err := hashPassword(strings.Repeat("a", 4097)); err == nil {
		t.Fatal("oversized password accepted")
	}
}

func TestPasswordConcurrencyBudget(t *testing.T) {
	for i := 0; i < cap(passwordWorkers); i++ {
		passwordWorkers <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(passwordWorkers); i++ {
			<-passwordWorkers
		}
	}()
	if _, err := hashPassword("test"); err == nil {
		t.Fatal("unbounded KDF admission")
	}
}
