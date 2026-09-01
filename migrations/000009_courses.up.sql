CREATE TABLE courses (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    name_normalized TEXT NOT NULL UNIQUE,
    description TEXT,
    curriculum TEXT,
    learning_goals TEXT,
    llm_instructions TEXT,
    language TEXT,
    is_active INTEGER NOT NULL DEFAULT 0 CHECK (is_active IN (0, 1)),
    created_at TEXT NOT NULL,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    activated_at TEXT,
    activated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    deactivated_at TEXT,
    deactivated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_at TEXT,
    updated_by TEXT REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE course_supervisors (
    course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    supervisor_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    assigned_at TEXT NOT NULL,
    assigned_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (course_id, supervisor_user_id)
);

CREATE INDEX course_supervisors_user_idx
    ON course_supervisors(supervisor_user_id, course_id);
