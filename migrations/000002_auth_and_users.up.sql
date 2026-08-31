CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    username_key TEXT NOT NULL UNIQUE,
    email TEXT,
    email_key TEXT UNIQUE,
    email_verified_at TEXT,
    password_hash TEXT NOT NULL,
    preferred_language TEXT NOT NULL,
    country TEXT NOT NULL,
    time_zone TEXT NOT NULL,
    security_generation INTEGER NOT NULL DEFAULT 1,
    must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1)),
    is_banned INTEGER NOT NULL DEFAULT 0 CHECK (is_banned IN (0, 1)),
    created_at TEXT NOT NULL
);
CREATE TABLE user_roles (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('administrator', 'supervisor', 'mentor', 'student')),
    granted_at TEXT NOT NULL,
    granted_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, role)
);
CREATE INDEX user_roles_role_idx ON user_roles(role, user_id);
CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    action TEXT NOT NULL,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    subject_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    metadata TEXT NOT NULL CHECK (json_valid(metadata))
);
