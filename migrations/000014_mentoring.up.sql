ALTER TABLE users ADD COLUMN mentoring_requests_allowed INTEGER NOT NULL DEFAULT 0
    CHECK (mentoring_requests_allowed IN (0, 1));

CREATE TABLE course_mentors (
    course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    mentor_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    assigned_at TEXT NOT NULL,
    assigned_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (course_id, mentor_user_id)
);
CREATE INDEX course_mentors_user_idx ON course_mentors(mentor_user_id, course_id);

CREATE TABLE mentor_assignments (
    course_id TEXT NOT NULL,
    student_user_id TEXT NOT NULL,
    mentor_user_id TEXT NOT NULL,
    assigned_at TEXT NOT NULL,
    assigned_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (course_id, student_user_id, mentor_user_id),
    FOREIGN KEY (course_id, student_user_id)
        REFERENCES course_students(course_id, student_user_id) ON DELETE CASCADE,
    FOREIGN KEY (course_id, mentor_user_id)
        REFERENCES course_mentors(course_id, mentor_user_id) ON DELETE CASCADE
);
CREATE INDEX mentor_assignments_mentor_idx
    ON mentor_assignments(course_id, mentor_user_id, student_user_id);

CREATE TABLE mentoring_sessions (
    id TEXT PRIMARY KEY,
    student_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    mentor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    topic TEXT NOT NULL,
    response TEXT,
    responded_at TEXT,
    responded_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    proposed_for TEXT,
    scheduled_for TEXT,
    meeting_instructions TEXT,
    meeting_url TEXT,
    created_at TEXT NOT NULL,
    closed_at TEXT,
    closed_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    closure_reason TEXT CHECK (closure_reason IN ('completed', 'cancelled')),
    CHECK ((response IS NULL) = (responded_at IS NULL)),
    CHECK ((closed_at IS NULL) = (closure_reason IS NULL))
);
CREATE INDEX mentoring_sessions_course_idx ON mentoring_sessions(course_id, created_at, id);
CREATE INDEX mentoring_sessions_student_idx ON mentoring_sessions(student_user_id, created_at, id);
CREATE INDEX mentoring_sessions_mentor_open_idx
    ON mentoring_sessions(mentor_user_id, closed_at, created_at, id);
