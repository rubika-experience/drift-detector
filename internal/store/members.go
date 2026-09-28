package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AddMember assigns userID to teamID with role. The unique index on
// team_members.user_id enforces one-team-per-user: this fails with a
// unique-violation error if the user is already on a team.
func AddMember(ctx context.Context, db *sql.DB, teamID, userID int64, role Role) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, $3)`,
		teamID, userID, role,
	)
	if err != nil {
		return fmt.Errorf("adding team member: %w", err)
	}
	return nil
}

// GetMembership returns the team and role for userID, or ErrNotFound if
// they aren't on a team yet (e.g. an admin created the user row but hasn't
// assigned a team — such a user cannot log in successfully).
func GetMembership(ctx context.Context, db *sql.DB, userID int64) (Membership, error) {
	var m Membership
	err := db.QueryRowContext(ctx, `
		SELECT tm.team_id, t.name, tm.role
		FROM team_members tm
		JOIN teams t ON t.id = tm.team_id
		WHERE tm.user_id = $1`, userID,
	).Scan(&m.TeamID, &m.TeamName, &m.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("looking up membership: %w", err)
	}
	return m, nil
}

// TeamMember is one row in a team's member list, for the admin UI.
type TeamMember struct {
	UserID int64
	Email  string
	Role   Role
}

// ListTeamMembers lists every user on teamID, for the admin UI's member list.
func ListTeamMembers(ctx context.Context, db *sql.DB, teamID int64) ([]TeamMember, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT u.id, u.email, tm.role
		FROM team_members tm
		JOIN users u ON u.id = tm.user_id
		WHERE tm.team_id = $1
		ORDER BY u.email`, teamID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing team members: %w", err)
	}
	defer rows.Close()

	var members []TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role); err != nil {
			return nil, fmt.Errorf("scanning team member: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// CountAdmins returns how many admin-role members teamID has, so callers
// can refuse to demote or remove the last one and lock the team out of its
// own admin panel.
func CountAdmins(ctx context.Context, db *sql.DB, teamID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM team_members WHERE team_id = $1 AND role = $2`,
		teamID, RoleAdmin,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting team admins: %w", err)
	}
	return n, nil
}

// UpdateMemberRole changes userID's role within teamID. Scoped to teamID so
// one team can't touch another's membership row even by guessing a user ID.
func UpdateMemberRole(ctx context.Context, db *sql.DB, teamID, userID int64, role Role) error {
	res, err := db.ExecContext(ctx,
		`UPDATE team_members SET role = $1 WHERE team_id = $2 AND user_id = $3`,
		role, teamID, userID,
	)
	if err != nil {
		return fmt.Errorf("updating member role: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveMember unassigns userID from teamID (the user row itself is kept —
// they simply have no team, and so can no longer log in successfully).
// Scoped to teamID so one team can't remove another's member.
func RemoveMember(ctx context.Context, db *sql.DB, teamID, userID int64) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`,
		teamID, userID,
	)
	if err != nil {
		return fmt.Errorf("removing member: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
