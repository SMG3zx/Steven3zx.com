package authentication

import (
	"strings"
	"testing"
)

func TestArgon2idHashingRejectsInvalidAndTamperedValues(t *testing.T) {
	password := "correct horse battery staple"
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected Argon2id parameters: %q", hash)
	}
	valid, err := verifyPassword(password, hash)
	if err != nil || !valid {
		t.Fatalf("verify valid password: valid=%v err=%v", valid, err)
	}
	valid, err = verifyPassword("wrong password", hash)
	if err != nil || valid {
		t.Fatalf("verify wrong password: valid=%v err=%v", valid, err)
	}
	if _, err := hashPassword("short"); err == nil {
		t.Fatal("short password was accepted")
	}
	if _, err := verifyPassword(password, "argon2id$v=19$m=999999,t=9,p=4$salt$hash"); err == nil {
		t.Fatal("unsafe encoded parameters were accepted")
	}
}
