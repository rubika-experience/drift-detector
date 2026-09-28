package store

import (
	"context"
	"database/sql"
	"fmt"
)

// CreateTeam inserts a team and returns its ID.
func CreateTeam(ctx context.Context, db *sql.DB, name string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO teams (name) VALUES ($1) RETURNING id`, name,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("creating team: %w", err)
	}
	return id, nil
}
