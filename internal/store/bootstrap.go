package store

import (
	"context"
	"database/sql"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// EnsureBootstrapAdmin creates the first team and its first admin user if,
// and only if, the users table is empty. This is how a brand-new database
// gets its first login: DRIFTGCP_ADMIN_EMAIL / DRIFTGCP_ADMIN_PASSWORD (set
// once), after which that admin manages further users from the dashboard
// and these env vars are no longer read.
func EnsureBootstrapAdmin(ctx context.Context, db *sql.DB, teamName, adminEmail, adminPassword string) error {
	count, err := CountUsers(ctx, db)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if adminEmail == "" || adminPassword == "" {
		return fmt.Errorf("database is empty and DRIFTGCP_ADMIN_EMAIL/DRIFTGCP_ADMIN_PASSWORD are not set; " +
			"set them once to create the first team and admin user")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing bootstrap admin password: %w", err)
	}

	teamID, err := CreateTeam(ctx, db, teamName)
	if err != nil {
		return err
	}
	userID, err := CreateUser(ctx, db, adminEmail, string(hash))
	if err != nil {
		return err
	}
	return AddMember(ctx, db, teamID, userID, RoleAdmin)
}
