CREATE TABLE course_students (
    id TEXT PRIMARY KEY,
    course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    student_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TEXT NOT NULL,
    added_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE (course_id, student_user_id)
);

CREATE INDEX course_students_student_idx
    ON course_students(student_user_id, course_id);
