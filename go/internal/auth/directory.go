// Package auth is the DEMO-ONLY identity provider shared by bulkwire-service (portal sign-in) and
// control-service (which signs in as demo users to generate load).
//
// The directory below mirrors the users seeded in each FI's Core database (sql/04-seed-lakeshore.sql,
// sql/05-seed-prairie.sql). It answers "who is this and which company/FI do they belong to"; Core remains
// the authority for roles, approval limits, and approval decisions. The e2e script verifies the two match.
//
// PRODUCTION NOTE: a real deployment signs users in with the FI's identity provider (OIDC); these services
// would only validate the tokens it issues.
package auth

import (
	"strings"
	"time"
	_ "time/tzdata" // WHY: embed the time-zone database so LoadLocation works in any container image
)

// Roles as stored in Core (dbo.Users.Role).
const (
	RoleInitiator = "Initiator"
	RoleApprover  = "Approver"
	RoleBankOps   = "BankOps"
)

// BankCompanyID is the Company ID bank or credit-union staff use at sign-in.
const BankCompanyID = "BANK"

// FI is one tenant (a bank or credit union) and its demo sign-in directory.
type FI struct {
	ID           string
	DisplayName  string
	ShortName    string // wordmark text
	PrimaryColor string
	AccentColor  string
	TimeZone     string // IANA zone of the FI's business day; "today" for process dates is the FI's local date
	Companies    []Company
}

// Location is the FI's time zone (UTC if the zone is unknown).
func (f FI) Location() *time.Location {
	if loc, err := time.LoadLocation(f.TimeZone); err == nil {
		return loc
	}
	return time.UTC
}

// LocalToday returns the FI's local calendar date at `now`, as midnight UTC of that date (the same form
// time.Parse(time.DateOnly, ...) produces), so it compares directly with parsed process dates.
// WHY: a wire dated for the customer's local "today" must not be rejected as past because UTC has already
// rolled over (after about 5 p.m. in US time zones).
func LocalToday(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Company is a commercial customer of the FI (or the FI's own staff, Company ID "BANK").
type Company struct {
	CompanyID  string // typed at sign-in
	CustomerID string // Core customer id; empty for bank staff
	Name       string
	Users      []User
}

// User is a demo user.
type User struct {
	UserID string
	Name   string
	Role   string
}

// Directory is the demo identity directory, keyed by fiId. All names are fictional.
var Directory = map[string]FI{
	"lakeshore": {
		ID: "lakeshore", DisplayName: "Lakeshore Community Bank", ShortName: "Lakeshore",
		PrimaryColor: "#0E2A47", AccentColor: "#C8962E", TimeZone: "America/Chicago",
		Companies: []Company{
			{CompanyID: "SUMMIT", CustomerID: "summit-logistics", Name: "Summit Logistics, Inc.", Users: []User{
				{UserID: "jordan", Name: "Jordan Ellis", Role: RoleInitiator},
				{UserID: "alex", Name: "Alex Rivera, Controller", Role: RoleApprover},
				{UserID: "sam", Name: "Sam Patel, Treasurer", Role: RoleApprover},
			}},
			{CompanyID: "NORTHLINE", CustomerID: "northline-medical", Name: "Northline Medical Group", Users: []User{
				{UserID: "taylor", Name: "Taylor Brooks", Role: RoleInitiator},
				{UserID: "quinn", Name: "Quinn Murphy, CFO", Role: RoleApprover},
				{UserID: "avery", Name: "Avery Collins, Controller", Role: RoleApprover},
			}},
			{CompanyID: BankCompanyID, Name: "Lakeshore Community Bank", Users: []User{
				{UserID: "morgan", Name: "Morgan Lee, Wire Operations", Role: RoleBankOps},
			}},
		},
	},
	"prairie": {
		ID: "prairie", DisplayName: "Prairie Valley Credit Union", ShortName: "Prairie Valley",
		PrimaryColor: "#1F4D3A", AccentColor: "#B5653A", TimeZone: "America/Denver",
		Companies: []Company{
			{CompanyID: "HARVEST", CustomerID: "harvest-coop", Name: "Harvest Growers Co-op", Users: []User{
				{UserID: "riley", Name: "Riley Chen", Role: RoleInitiator},
				{UserID: "casey", Name: "Casey Ortiz", Role: RoleApprover},
				{UserID: "jamie", Name: "Jamie Fields", Role: RoleApprover},
			}},
			{CompanyID: BankCompanyID, Name: "Prairie Valley Credit Union", Users: []User{
				{UserID: "devon", Name: "Devon Brooks, Member Operations", Role: RoleBankOps},
			}},
		},
	},
}

// Identity is a resolved directory entry: the user plus their company and FI.
type Identity struct {
	FiID        string
	CompanyID   string
	CustomerID  string // empty for bank staff
	CompanyName string
	UserID      string
	Name        string
	Role        string
}

// Lookup resolves a user at a given FI by Company ID and User ID (both case-insensitive). A user of
// another FI is never found here: each portal only signs in its own FI's users.
func Lookup(fiID, companyID, userID string) (Identity, bool) {
	fi, ok := Directory[fiID]
	if !ok {
		return Identity{}, false
	}
	for _, c := range fi.Companies {
		if !strings.EqualFold(c.CompanyID, strings.TrimSpace(companyID)) {
			continue
		}
		for _, u := range c.Users {
			if strings.EqualFold(u.UserID, strings.TrimSpace(userID)) {
				return Identity{FiID: fi.ID, CompanyID: c.CompanyID, CustomerID: c.CustomerID, CompanyName: c.Name,
					UserID: u.UserID, Name: u.Name, Role: u.Role}, true
			}
		}
	}
	return Identity{}, false
}

// FindUser resolves a user by FI and user ID alone (used by control-service to act as a demo user).
func FindUser(fiID, userID string) (Identity, bool) {
	fi, ok := Directory[fiID]
	if !ok {
		return Identity{}, false
	}
	for _, c := range fi.Companies {
		for _, u := range c.Users {
			if u.UserID == userID {
				return Lookup(fiID, c.CompanyID, userID)
			}
		}
	}
	return Identity{}, false
}

// CustomerName returns the display name of an FI's customer.
func CustomerName(fiID, customerID string) string {
	for _, c := range Directory[fiID].Companies {
		if c.CustomerID != "" && c.CustomerID == customerID {
			return c.Name
		}
	}
	return customerID
}
