-- Initial schema for driftgcp's Postgres-backed auth: users, teams,
-- team membership (one team per user, enforced by the unique index on
-- team_members.user_id), durable sessions, and per-team state file
-- assignments for the dashboard's state-file dropdown.

CREATE TABLE IF NOT EXISTS teams (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per user: the unique constraint on user_id is what makes this
-- "one team per user" rather than a general many-to-many join table.
CREATE TABLE IF NOT EXISTS team_members (
    team_id BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    role    TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at);

-- Replaces the dashboard's hardcoded state-file dropdown: each team sees
-- only the state files assigned to it here.
CREATE TABLE IF NOT EXISTS team_state_files (
    id      BIGSERIAL PRIMARY KEY,
    team_id BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    label   TEXT NOT NULL,
    path    TEXT NOT NULL,
    UNIQUE (team_id, path)
);
