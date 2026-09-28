package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// CreateSession inserts a new session row with a random token and returns
// it. Sessions live in Postgres (not memory), so they survive a server
// restart.
func CreateSession(ctx context.Context, db *sql.DB, userID int64, ttl time.Duration) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, time.Now().Add(ttl),
	)
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return token, nil
}

// GetSessionUserID returns the user ID for an unexpired session token, or
// ErrNotFound if the token doesn't exist or has expired.
func GetSessionUserID(ctx context.Context, db *sql.DB, token string) (int64, error) {
	var userID int64
	err := db.QueryRowContext(ctx,
		`SELECT user_id FROM sessions WHERE token = $1 AND expires_at > now()`, token,
	).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("looking up session: %w", err)
	}
	return userID, nil
}

// DeleteSession removes a session row (logout).
func DeleteSession(ctx context.Context, db *sql.DB, token string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE token = $1`, token); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
