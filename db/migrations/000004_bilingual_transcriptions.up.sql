ALTER TABLE transcriptions
    ADD COLUMN detected_language TEXT,
    ADD COLUMN target_language TEXT,
    ADD COLUMN translated_text TEXT;

ALTER TABLE transcriptions DROP CONSTRAINT transcriptions_check;

ALTER TABLE transcriptions
    ADD CONSTRAINT transcriptions_state_result_check CHECK (
        (status = 'COMPLETED'
            AND transcript IS NOT NULL
            AND detected_language IN ('so', 'en')
            AND target_language IN ('so', 'en')
            AND detected_language <> target_language
            AND translated_text IS NOT NULL
            AND failure_reason IS NULL
            AND completed_at IS NOT NULL)
        OR (status = 'FAILED'
            AND transcript IS NULL
            AND detected_language IS NULL
            AND target_language IS NULL
            AND translated_text IS NULL
            AND failure_reason IS NOT NULL)
        OR (status IN ('PENDING', 'PROCESSING')
            AND transcript IS NULL
            AND detected_language IS NULL
            AND target_language IS NULL
            AND translated_text IS NULL
            AND failure_reason IS NULL)
    );
