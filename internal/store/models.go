package store

import "time"

// Role is a user's role within their team.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// User is a row in the users table.
type User struct {
	ID           int64
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// Team is a row in the teams table.
type Team struct {
	ID   int64
	Name string
}

// Membership is a user's (at most one, per the one-team-per-user design)
// row in team_members, joined with the team's name.
type Membership struct {
	TeamID   int64
	TeamName string
	Role     Role
}

// StateFile is a row in team_state_files: one entry in a team's Terraform
// state-file dropdown on the dashboard.
type StateFile struct {
	ID    int64
	Label string
	Path  string
}
