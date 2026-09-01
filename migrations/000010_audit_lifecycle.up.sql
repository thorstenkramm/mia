CREATE TABLE audit_events_rebuilt (
    id TEXT PRIMARY KEY,
    action TEXT NOT NULL,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    subject_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    metadata TEXT CHECK (metadata IS NULL OR json_valid(metadata)),
    course_id TEXT,
    subject_type TEXT CHECK (subject_type IS NULL OR subject_type = 'course'),
    subject_fingerprint TEXT
);

INSERT INTO audit_events_rebuilt
    (id, action, actor_user_id, subject_user_id, created_at, metadata, course_id)
SELECT id, action, actor_user_id, subject_user_id, created_at,
       json_remove(metadata, '$.course_id'),
       json_extract(metadata, '$.course_id')
FROM audit_events;

DROP TABLE audit_events;
ALTER TABLE audit_events_rebuilt RENAME TO audit_events;
CREATE INDEX audit_events_course_idx ON audit_events(course_id, created_at, id);
