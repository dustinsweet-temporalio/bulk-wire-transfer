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

// "Today" for process dates is the FI's local date: at 6 p.m. in Chicago it is already tomorrow in UTC, but a
// wire dated for the customer's today must not count as past.
func TestLocalTodayUsesTheFIsTimeZone(t *testing.T) {
	loc := Directory["lakeshore"].Location()
	evening := time.Date(2026, 10, 29, 18, 0, 0, 0, loc) // 23:00 UTC in October (CDT)
	if got := LocalToday(evening.Add(2*time.Hour), loc); got != time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("local today at 8 p.m. Chicago = %v, want 2026-10-29", got)
	}
	if loc.String() != "America/Chicago" || Directory["prairie"].Location().String() != "America/Denver" {
		t.Fatalf("zones: %v %v", loc, Directory["prairie"].Location())
	}
}
