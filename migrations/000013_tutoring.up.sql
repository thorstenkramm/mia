ALTER TABLE users ADD COLUMN year_of_birth INTEGER CHECK (year_of_birth IS NULL OR year_of_birth BETWEEN 1900 AND 9999);
ALTER TABLE users ADD COLUMN llm_instructions TEXT;

CREATE TABLE tutoring_sessions (
    id TEXT PRIMARY KEY,
    course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    student_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'completed')),
    client_request_id TEXT NOT NULL,
    request_digest BLOB NOT NULL CHECK (length(request_digest) = 32),
    summary TEXT,
    follow_up TEXT,
    summary_source TEXT CHECK (summary_source IS NULL OR summary_source IN ('generated', 'supervisor')),
    summary_updated_at TEXT,
    summary_updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    started_at TEXT NOT NULL,
    last_activity_at TEXT NOT NULL,
    completed_at TEXT,
    completed_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    CHECK ((state = 'completed') = (completed_at IS NOT NULL)),
    CHECK ((state = 'completed') = (completed_by IS NOT NULL)),
    CHECK (completed_by IS NULL OR completed_by = student_user_id),
    CHECK (summary IS NULL OR state = 'completed'),
    CHECK ((summary IS NULL AND follow_up IS NULL AND summary_source IS NULL AND summary_updated_at IS NULL) OR
           (summary IS NOT NULL AND follow_up IS NOT NULL AND summary_source IS NOT NULL AND summary_updated_at IS NOT NULL)),
    UNIQUE (student_user_id, client_request_id),
    FOREIGN KEY (course_id, student_user_id)
        REFERENCES course_students(course_id, student_user_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX tutoring_sessions_one_active_student_idx ON tutoring_sessions(student_user_id)
    WHERE state = 'active';
CREATE INDEX tutoring_sessions_course_student_idx
    ON tutoring_sessions(course_id, student_user_id, started_at DESC, id DESC);

CREATE TABLE session_material_selections (
    tutoring_session_id TEXT NOT NULL REFERENCES tutoring_sessions(id) ON DELETE CASCADE,
    material_id TEXT NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    selected_at TEXT NOT NULL,
    PRIMARY KEY (tutoring_session_id, material_id)
);

CREATE TABLE student_messages (
    id TEXT PRIMARY KEY,
    tutoring_session_id TEXT NOT NULL REFERENCES tutoring_sessions(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    content TEXT NOT NULL,
    client_request_id TEXT NOT NULL,
    request_digest BLOB NOT NULL CHECK (length(request_digest) = 32),
    created_at TEXT NOT NULL,
    UNIQUE (id, tutoring_session_id),
    UNIQUE (tutoring_session_id, sequence),
    UNIQUE (tutoring_session_id, client_request_id)
);

CREATE TABLE tutor_responses (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES tutoring_sessions(id) ON DELETE CASCADE,
    student_message_id TEXT NOT NULL,
    retry_of_response_id TEXT REFERENCES tutor_responses(id) ON DELETE SET NULL,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    state TEXT NOT NULL CHECK (state IN ('queued', 'generating', 'completed', 'interrupted', 'failed')),
    content TEXT NOT NULL DEFAULT '',
    failure_code TEXT,
    provider TEXT,
    model TEXT,
    input_units INTEGER NOT NULL DEFAULT 0 CHECK (input_units >= 0),
    output_units INTEGER NOT NULL DEFAULT 0 CHECK (output_units >= 0),
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    CHECK ((state = 'queued' AND started_at IS NULL AND finished_at IS NULL AND provider IS NULL AND model IS NULL) OR
           (state = 'generating' AND started_at IS NOT NULL AND finished_at IS NULL) OR
           (state IN ('completed', 'interrupted', 'failed') AND finished_at IS NOT NULL)),
    UNIQUE (id, session_id),
    UNIQUE (student_message_id, attempt),
    FOREIGN KEY (student_message_id, session_id)
        REFERENCES student_messages(id, tutoring_session_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX tutor_responses_one_generating_idx ON tutor_responses(session_id)
    WHERE state = 'generating';
CREATE UNIQUE INDEX tutor_responses_one_queued_idx ON tutor_responses(session_id)
    WHERE state = 'queued';
CREATE INDEX tutor_responses_session_idx ON tutor_responses(session_id, created_at, id);

CREATE TABLE material_retrievals (
    id TEXT PRIMARY KEY,
    tutoring_session_id TEXT NOT NULL REFERENCES tutoring_sessions(id) ON DELETE CASCADE,
    tutor_response_id TEXT NOT NULL REFERENCES tutor_responses(id) ON DELETE CASCADE,
    material_id TEXT NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    chapter_label TEXT,
    section_label TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (tutor_response_id, material_id),
    FOREIGN KEY (tutor_response_id, tutoring_session_id)
        REFERENCES tutor_responses(id, session_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX jobs_active_tutoring_summary_idx ON jobs(subject_id)
    WHERE type = 'tutoring-session-summary' AND state IN ('queued', 'running');
