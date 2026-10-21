package natsauth

import (
	"testing"

	"github.com/nats-io/jwt/v2"
)

func TestAnOperatorAndAccountRestoredFromTheirSeedsKeepTheirIdentity(t *testing.T) {
	opSeed, err := NewOperatorSeed()
	if err != nil {
		t.Fatal(err)
	}
	accSeed, err := NewAccountSeed()
	if err != nil {
		t.Fatal(err)
	}

	op, err := OperatorFromSeed(opSeed, "janus")
	if err != nil {
		t.Fatal(err)
	}
	acc, err := AccountFromSeed(op, accSeed, "AGENTS")
	if err != nil {
		t.Fatal(err)
	}

	// A restart restores the same keys from the same seeds.
	op2, err := OperatorFromSeed(opSeed, "janus")
	if err != nil {
		t.Fatal(err)
	}
	acc2, err := AccountFromSeed(op2, accSeed, "AGENTS")
	if err != nil {
		t.Fatal(err)
	}
	if op2.Claims.Subject != op.Claims.Subject || acc2.Pub != acc.Pub {
		t.Fatal("restored keys differ from the originals")
	}

	// The restored account JWT is signed by the restored operator.
	claims, err := jwt.DecodeAccountClaims(acc2.JWT)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != op2.Claims.Subject || claims.Subject != acc2.Pub {
		t.Fatalf("account JWT issued by %s for %s", claims.Issuer, claims.Subject)
	}
}

func TestAUserIssuedByARestoredAccountIsAcceptedByItsOperator(t *testing.T) {
	opSeed, err := NewOperatorSeed()
	if err != nil {
		t.Fatal(err)
	}
	accSeed, err := NewAccountSeed()
	if err != nil {
		t.Fatal(err)
	}
	op, err := OperatorFromSeed(opSeed, "janus")
	if err != nil {
		t.Fatal(err)
	}
	acc, err := AccountFromSeed(op, accSeed, "AGENTS")
	if err != nil {
		t.Fatal(err)
	}
	creds, err := acc.IssueLeafUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := jwt.DecodeUserClaims(creds.JWT)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != acc.Pub {
		t.Fatalf("leaf user issued by %s, want %s", claims.Issuer, acc.Pub)
	}
}
