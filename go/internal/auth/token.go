// Portal sign-in tokens: HS256 JWTs signed with AUTH_SIGNING_KEY. bulkwire-service mints them at sign-in;
// bulkwire-service and each FI's Core validate them. Every handler takes the user, customer, FI, and role
// from these claims, never from request bodies.
package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenLifetime is how long a portal session lasts.
const TokenLifetime = 8 * time.Hour

// Claims are the token's claims (names shared with core-api's DemoTokenValidator).
type Claims struct {
	FiID       string `json:"fi"`
	CustomerID string `json:"cust,omitempty"`
	Role       string `json:"role"`
	Name       string `json:"name"`
	jwt.RegisteredClaims
}

// UserID is the subject.
func (c Claims) UserID() string { return c.Subject }

// IsBankOps reports whether the user is bank or credit-union staff.
func (c Claims) IsBankOps() bool { return c.Role == RoleBankOps }

// ErrInvalidToken is returned for any token that does not validate.
var ErrInvalidToken = errors.New("invalid or expired sign-in token")

// Tokens mints and validates portal tokens.
type Tokens struct {
	key []byte
	now func() time.Time
}

// NewTokens returns a token service using the shared signing key.
func NewTokens(signingKey string) *Tokens {
	return &Tokens{key: []byte(signingKey), now: time.Now}
}

// Mint issues a token for a resolved identity.
func (t *Tokens) Mint(id Identity) (string, error) {
	now := t.now()
	claims := Claims{
		FiID: id.FiID, CustomerID: id.CustomerID, Role: id.Role, Name: id.Name,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   id.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenLifetime)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.key)
}

// Validate checks signature, algorithm, and expiry, and returns the claims.
func (t *Tokens) Validate(token string) (Claims, error) {
	var c Claims
	parsed, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return t.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil || !parsed.Valid || c.Subject == "" || c.FiID == "" {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return c, nil
}

// PasswordMatches compares the demo password in constant time.
func PasswordMatches(presented, expected string) bool {
	return subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}
