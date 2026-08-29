CREATE TABLE transcriptions (
    id UUID PRIMARY KEY,
    content_sha256 TEXT NOT NULL UNIQUE CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    original_filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    byte_size BIGINT NOT NULL CHECK (byte_size > 0),
    storage_path TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'so',
    status TEXT NOT NULL CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')),
    transcript TEXT,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processing_started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    CHECK (
        (status = 'COMPLETED' AND transcript IS NOT NULL AND failure_reason IS NULL AND completed_at IS NOT NULL)
        OR (status = 'FAILED' AND transcript IS NULL AND failure_reason IS NOT NULL)
        OR (status IN ('PENDING', 'PROCESSING') AND transcript IS NULL AND failure_reason IS NULL)
    )
);

CREATE INDEX transcriptions_pending_idx ON transcriptions (created_at) WHERE status = 'PENDING';

COMMENT ON TABLE transcriptions IS 'Audio metadata and transcription results; audio bytes remain in filesystem or object storage.';
