CREATE TABLE materials (
    id TEXT PRIMARY KEY,
    course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    owner_user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    scope TEXT NOT NULL CHECK (scope IN ('course-wide', 'student-private')),
    name TEXT NOT NULL,
    name_normalized TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('text-book', 'exam', 'worksheet', 'website', 'youtube')),
    format TEXT NOT NULL CHECK (format IN ('pdf', 'jpeg', 'png', 'text', 'markdown', 'docx', 'link')),
    state TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'processing', 'ready', 'failed')),
    external_url TEXT,
    brief_json TEXT CHECK (brief_json IS NULL OR json_valid(brief_json)),
    brief_source TEXT CHECK (brief_source IS NULL OR brief_source IN ('generated', 'supervisor')),
    brief_updated_at TEXT,
    brief_updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    is_approved INTEGER NOT NULL DEFAULT 0 CHECK (is_approved IN (0, 1)),
    approved_at TEXT,
    approved_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    failure_code TEXT,
    created_at TEXT NOT NULL,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_at TEXT,
    CHECK ((scope = 'student-private') = (owner_user_id IS NOT NULL)),
    CHECK ((format = 'link') = (external_url IS NOT NULL)),
    CHECK (scope = 'course-wide' OR is_approved = 0),
    UNIQUE (course_id, name_normalized)
);

CREATE INDEX materials_owner_idx ON materials(owner_user_id, course_id);
CREATE INDEX materials_ready_idx ON materials(course_id, state, is_approved, format);

CREATE TABLE material_files (
    id TEXT PRIMARY KEY,
    material_id TEXT NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    original_filename TEXT NOT NULL,
    media_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes > 0),
    page_count INTEGER NOT NULL CHECK (page_count >= 0),
    state TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'processing', 'processed', 'failed')),
    failure_code TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (material_id, original_filename)
);

CREATE INDEX material_files_material_idx ON material_files(material_id, created_at, id);

CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('material-extraction', 'material-summary', 'tutoring-session-summary')),
    subject_type TEXT NOT NULL CHECK (subject_type IN ('material-file', 'material', 'tutoring-session')),
    subject_id TEXT NOT NULL,
    course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    owner_user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    available_at TEXT NOT NULL,
    lease_token TEXT,
    lease_expires_at TEXT,
    failure_code TEXT,
    provider_input_units INTEGER NOT NULL DEFAULT 0 CHECK (provider_input_units >= 0),
    provider_output_units INTEGER NOT NULL DEFAULT 0 CHECK (provider_output_units >= 0),
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    CHECK ((state = 'running') = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL))
);

CREATE INDEX jobs_due_idx ON jobs(state, available_at, created_at, id);
CREATE INDEX jobs_subject_idx ON jobs(subject_type, subject_id, created_at, id);
CREATE INDEX jobs_course_idx ON jobs(course_id, created_at, id);
CREATE UNIQUE INDEX jobs_active_extraction_idx ON jobs(subject_id)
    WHERE type = 'material-extraction' AND state IN ('queued', 'running');
CREATE UNIQUE INDEX jobs_active_material_summary_idx ON jobs(subject_id)
    WHERE type = 'material-summary' AND state IN ('queued', 'running');
