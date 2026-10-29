// Tests for demo sign-in tokens: round trip, expiry and wrong key, and that each portal admits only its own FI's users.

package auth

import (
	"errors"
	"testing"
	"time"
)

const key = "test-signing-key-test-signing-key-0123456789"

func TestMintAndValidateRoundTrip(t *testing.T) {
	id, ok := Lookup("lakeshore", "summit", "JORDAN")
	if !ok {
		t.Fatal("jordan not found")
	}
	tok, err := NewTokens(key).Mint(id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewTokens(key).Validate(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID() != "jordan" || c.FiID != "lakeshore" || c.CustomerID != "summit-logistics" || c.Role != RoleInitiator {
		t.Fatalf("claims: %+v", c)
	}
}

func TestExpiredAndWrongKeyAreRejected(t *testing.T) {
	id, _ := Lookup("prairie", "HARVEST", "riley")
	old := NewTokens(key)
	old.now = func() time.Time { return time.Now().Add(-9 * time.Hour) }
	expired, _ := old.Mint(id)
	if _, err := NewTokens(key).Validate(expired); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token accepted: %v", err)
	}
	tok, _ := NewTokens(key).Mint(id)
	if _, err := NewTokens(key + "x").Validate(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong key accepted: %v", err)
	}
}

func TestUsersOfAnotherFICannotSignInHere(t *testing.T) {
	if _, ok := Lookup("prairie", "SUMMIT", "jordan"); ok {
		t.Fatal("a Lakeshore user resolved at Prairie")
	}
	if id, ok := Lookup("prairie", "bank", "devon"); !ok || id.Role != RoleBankOps || id.CustomerID != "" {
		t.Fatalf("devon: %+v %v", id, ok)
	}
}

func TestTokenCarriesTheFIClaim(t *testing.T) {
	// Core refuses a token whose fi claim is another FI; make sure the claim is always set from the directory.
	id, _ := FindUser("lakeshore", "morgan")
	tok, _ := NewTokens(key).Mint(id)
	c, _ := NewTokens(key).Validate(tok)
	if c.FiID != "lakeshore" || !c.IsBankOps() {
		t.Fatalf("claims: %+v", c)
	}
}
