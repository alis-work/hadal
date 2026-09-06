UPDATE transcriptions
SET status = 'FAILED', transcript = NULL, detected_language = NULL,
    target_language = NULL, translated_text = NULL,
    failure_reason = 'unsupported language', completed_at = NULL,
    updated_at = NOW()
WHERE status = 'COMPLETED' AND detected_language = 'unsupported';

UPDATE text_translations
SET status = 'FAILED', detected_language = NULL, target_language = NULL,
    interpreted_source_text = NULL, translated_text = NULL,
    clarification_required = FALSE, clarification_question = NULL,
    failure_reason = 'unsupported language', completed_at = NULL,
    next_attempt_at = NULL, updated_at = NOW()
WHERE status = 'COMPLETED' AND detected_language = 'unsupported';

ALTER TABLE transcriptions
    DROP CONSTRAINT transcriptions_state_result_check;

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

ALTER TABLE text_translations
    DROP CONSTRAINT text_translations_language_pair_check,
    DROP CONSTRAINT text_translations_state_result_check;

ALTER TABLE text_translations
    ADD CONSTRAINT text_translations_language_pair_check CHECK (
        (detected_language IS NULL AND target_language IS NULL)
        OR (detected_language IS NOT NULL
            AND target_language IS NOT NULL
            AND detected_language IN ('so', 'en')
            AND target_language IN ('so', 'en')
            AND detected_language <> target_language)
    ),
    ADD CONSTRAINT text_translations_state_result_check CHECK (
        (status = 'COMPLETED'
            AND provider_attempt_count > 0
            AND processing_started_at IS NOT NULL
            AND completed_at IS NOT NULL
            AND next_attempt_at IS NULL
            AND failure_reason IS NULL
            AND (
                (clarification_required = FALSE
                    AND detected_language IS NOT NULL
                    AND target_language IS NOT NULL
                    AND translated_text IS NOT NULL
                    AND clarification_question IS NULL)
                OR (clarification_required = TRUE
                    AND translated_text IS NULL
                    AND clarification_question IS NOT NULL)
            ))
        OR (status = 'FAILED'
            AND provider_attempt_count > 0
            AND processing_started_at IS NOT NULL
            AND completed_at IS NULL
            AND next_attempt_at IS NULL
            AND detected_language IS NULL
            AND target_language IS NULL
            AND interpreted_source_text IS NULL
            AND translated_text IS NULL
            AND clarification_required = FALSE
            AND clarification_question IS NULL
            AND failure_reason IS NOT NULL)
        OR (status = 'PENDING'
            AND processing_started_at IS NULL
            AND completed_at IS NULL
            AND next_attempt_at IS NOT NULL
            AND detected_language IS NULL
            AND target_language IS NULL
            AND interpreted_source_text IS NULL
            AND translated_text IS NULL
            AND clarification_required = FALSE
            AND clarification_question IS NULL
            AND failure_reason IS NULL)
        OR (status = 'PROCESSING'
            AND provider_attempt_count > 0
            AND processing_started_at IS NOT NULL
            AND completed_at IS NULL
            AND next_attempt_at IS NULL
            AND detected_language IS NULL
            AND target_language IS NULL
            AND interpreted_source_text IS NULL
            AND translated_text IS NULL
            AND clarification_required = FALSE
            AND clarification_question IS NULL
            AND failure_reason IS NULL)
    );
