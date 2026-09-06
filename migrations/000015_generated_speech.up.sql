CREATE TABLE generated_speech (
    id TEXT PRIMARY KEY CHECK (id GLOB 'sp_*' AND instr(id, '/') = 0 AND instr(id, '\') = 0),
    tutor_response_id TEXT NOT NULL REFERENCES tutor_responses(id) ON DELETE CASCADE,
    voice_id TEXT NOT NULL CHECK (length(voice_id) BETWEEN 1 AND 128 AND voice_id NOT GLOB '*[^ -~]*'),
    source_content_hash BLOB NOT NULL CHECK (length(source_content_hash) = 32),
    state TEXT NOT NULL CHECK (state IN ('generating', 'available', 'failed')),
    generated_at TEXT,
    expires_at TEXT,
    failure_code TEXT,
    CHECK ((state = 'generating' AND generated_at IS NULL AND expires_at IS NULL AND failure_code IS NULL) OR
           (state = 'available' AND generated_at IS NOT NULL AND expires_at IS NOT NULL AND failure_code IS NULL) OR
           (state = 'failed' AND generated_at IS NULL AND expires_at IS NULL AND failure_code IS NOT NULL)),
    CHECK (expires_at IS NULL OR expires_at > generated_at),
    UNIQUE (tutor_response_id, source_content_hash, voice_id)
);

CREATE INDEX generated_speech_expiry_idx ON generated_speech(expires_at) WHERE state = 'available';
