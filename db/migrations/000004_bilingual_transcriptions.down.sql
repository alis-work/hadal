ALTER TABLE transcriptions DROP CONSTRAINT transcriptions_state_result_check;

ALTER TABLE transcriptions
    DROP COLUMN translated_text,
    DROP COLUMN target_language,
    DROP COLUMN detected_language;

ALTER TABLE transcriptions
    ADD CONSTRAINT transcriptions_check CHECK (
        (status = 'COMPLETED' AND transcript IS NOT NULL AND failure_reason IS NULL AND completed_at IS NOT NULL)
        OR (status = 'FAILED' AND transcript IS NULL AND failure_reason IS NOT NULL)
        OR (status IN ('PENDING', 'PROCESSING') AND transcript IS NULL AND failure_reason IS NULL)
    );
