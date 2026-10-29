// Sign-in, branding, and the two kinds of caller authentication.
//
// DEMO-ONLY identity provider: users sign in with Company ID + User ID + the shared demo password and get an
// HS256 token. Every handler takes fiId / customerId / role from the token, never from request bodies, and
// answers 404 for another FI's or customer's batches.
// PRODUCTION NOTE: the FI's identity provider (OIDC) issues tokens; this service would only validate them,
// and service-to-service calls would use mTLS or workload identity instead of a shared internal token.
package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"bulkwire/internal/auth"
)

type claimsKey struct{}

// user wraps a handler that requires a portal sign-in token.
func (s *Server) user(h func(http.ResponseWriter, *http.Request, auth.Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "sign in again")
			return
		}
		claims, err := s.Tokens.Validate(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "sign in again")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)), claims)
	}
}

// internal wraps a service-to-service handler (core-api callbacks, control-service hooks).
func (s *Server) internal(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Internal-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.Cfg.Core.InternalToken)) != 1 || got == "" {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid internal token")
			return
		}
		h(w, r)
	}
}

type demoUser struct {
	CompanyID string `json:"companyId"`
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	Role      string `json:"role"`
}

// branding is public: the sign-in page themes itself per origin (window.FI_ID from the gateway).
func (s *Server) branding(w http.ResponseWriter, r *http.Request) {
	fi, ok := auth.Directory[r.PathValue("fiId")]
	if !ok {
		writeError(w, http.StatusNotFound, "UNKNOWN_FI", "unknown financial institution")
		return
	}
	var users []demoUser
	for _, c := range fi.Companies {
		for _, u := range c.Users {
			users = append(users, demoUser{CompanyID: c.CompanyID, UserID: u.UserID, Name: u.Name, Role: u.Role})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fiId": fi.ID, "displayName": fi.DisplayName, "wordmark": fi.ShortName,
		"primaryColor": fi.PrimaryColor, "accentColor": fi.AccentColor,
		"demoUsers": users, // DEMO-ONLY sign-in hint
	})
}

type userView struct {
	UserID      string `json:"userId"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	FiID        string `json:"fiId"`
	CustomerID  string `json:"customerId"`
	CompanyID   string `json:"companyId"`
	CompanyName string `json:"companyName"`
}

func viewOf(id auth.Identity) userView {
	return userView{UserID: id.UserID, Name: id.Name, Role: id.Role, FiID: id.FiID, CustomerID: id.CustomerID,
		CompanyID: id.CompanyID, CompanyName: id.CompanyName}
}

// login: 401 on any mismatch, including a user of another FI signing in at this FI's portal.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FiID      string `json:"fiId"`
		CompanyID string `json:"companyId"`
		UserID    string `json:"userId"`
		Password  string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid sign-in request")
		return
	}
	id, ok := auth.Lookup(req.FiID, req.CompanyID, req.UserID)
	if !ok || !auth.PasswordMatches(req.Password, s.Cfg.DemoPassword) {
		writeError(w, http.StatusUnauthorized, "SIGN_IN_FAILED", "We couldn't sign you in. Check your Company ID, User ID, and password.")
		return
	}
	token, err := s.Tokens.Mint(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "could not issue a token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": viewOf(id)})
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, c auth.Claims) {
	id, ok := auth.FindUser(c.FiID, c.UserID())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "sign in again")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(id))
}
