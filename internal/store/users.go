package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound is returned by lookups that find no matching row.
var ErrNotFound = errors.New("not found")

// CreateUser inserts a user with an already-hashed password and returns its
// ID. Callers hash the password (bcrypt) before calling this.
func CreateUser(ctx context.Context, db *sql.DB, email, passwordHash string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
		email, passwordHash,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("creating user: %w", err)
	}
	return id, nil
}

// GetUserByEmail looks up a user by email, or ErrNotFound.
func GetUserByEmail(ctx context.Context, db *sql.DB, email string) (User, error) {
	var u User
	err := db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("looking up user by email: %w", err)
	}
	return u, nil
}

// GetUserByID looks up a user by ID, or ErrNotFound.
func GetUserByID(ctx context.Context, db *sql.DB, id int64) (User, error) {
	var u User
	err := db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("looking up user by id: %w", err)
	}
	return u, nil
}

// CountUsers returns the total number of users, used to decide whether
// bootstrap needs to create the first admin.
func CountUsers(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return n, nil
}
