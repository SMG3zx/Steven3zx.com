package authentication

import (
	"context"
	"strings"
	"testing"
	"time"
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

func TestSessionTokensRoundTripAndRejectOtherIssuers(t *testing.T) {
	ctx := context.Background()
	secret := []byte(strings.Repeat("k", 32))
	service := New(secret, "janus-test", time.Minute)
	token, err := service.IssueToken(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if subject, valid := service.ValidateToken(ctx, token.Value); !valid || subject != "alice" {
		t.Fatalf("validate own token = %q, %v", subject, valid)
	}
	if _, valid := New(secret, "another-issuer", time.Minute).ValidateToken(ctx, token.Value); valid {
		t.Fatal("a token for another issuer was accepted")
	}
	if _, valid := New([]byte(strings.Repeat("x", 32)), "janus-test", time.Minute).ValidateToken(ctx, token.Value); valid {
		t.Fatal("a token signed with another secret was accepted")
	}
	if _, valid := service.ValidateToken(ctx, "not-a-token"); valid {
		t.Fatal("garbage was accepted as a token")
	}
	if _, err := service.IssueToken(ctx, "  "); err == nil {
		t.Fatal("a token was issued without a subject")
	}
	expired := New(secret, "janus-test", -time.Minute)
	old, err := expired.IssueToken(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, valid := expired.ValidateToken(ctx, old.Value); valid {
		t.Fatal("an expired token was accepted")
	}
}
