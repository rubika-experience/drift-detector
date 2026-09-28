package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ListStateFiles lists teamID's assigned Terraform state files, for the
// dashboard's state-file dropdown.
func ListStateFiles(ctx context.Context, db *sql.DB, teamID int64) ([]StateFile, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, label, path FROM team_state_files WHERE team_id = $1 ORDER BY label`, teamID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing state files: %w", err)
	}
	defer rows.Close()

	var files []StateFile
	for rows.Next() {
		var f StateFile
		if err := rows.Scan(&f.ID, &f.Label, &f.Path); err != nil {
			return nil, fmt.Errorf("scanning state file: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// CreateStateFile assigns a state file to teamID and returns its ID.
func CreateStateFile(ctx context.Context, db *sql.DB, teamID int64, label, path string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO team_state_files (team_id, label, path) VALUES ($1, $2, $3) RETURNING id`,
		teamID, label, path,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("creating state file: %w", err)
	}
	return id, nil
}

// DeleteStateFile removes a state file, scoped to teamID so one team can't
// delete another's rows even if it guesses an ID.
func DeleteStateFile(ctx context.Context, db *sql.DB, teamID, id int64) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM team_state_files WHERE id = $1 AND team_id = $2`, id, teamID,
	)
	if err != nil {
		return fmt.Errorf("deleting state file: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// HasStateFile reports whether path is assigned to teamID — used to
// enforce that a scan can only run against a state file the requesting
// user's team actually owns.
func HasStateFile(ctx context.Context, db *sql.DB, teamID int64, path string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM team_state_files WHERE team_id = $1 AND path = $2)`, teamID, path,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking state file ownership: %w", err)
	}
	return exists, nil
}
