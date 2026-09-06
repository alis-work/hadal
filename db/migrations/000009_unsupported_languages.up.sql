ALTER TABLE transcriptions
    DROP CONSTRAINT transcriptions_state_result_check;

ALTER TABLE transcriptions
    ADD CONSTRAINT transcriptions_state_result_check CHECK (
        (status = 'COMPLETED'
            AND transcript IS NOT NULL
            AND failure_reason IS NULL
            AND completed_at IS NOT NULL
            AND (
                (detected_language IN ('so', 'en')
                    AND target_language IN ('so', 'en')
                    AND detected_language <> target_language
                    AND translated_text IS NOT NULL)
                OR (detected_language = 'unsupported'
                    AND target_language IS NULL
                    AND translated_text IS NULL)
            ))
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
        OR (detected_language IN ('so', 'en')
            AND target_language IN ('so', 'en')
            AND detected_language <> target_language)
        OR (detected_language = 'unsupported' AND target_language IS NULL)
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
                    AND detected_language IN ('so', 'en')
                    AND target_language IN ('so', 'en')
                    AND translated_text IS NOT NULL
                    AND clarification_question IS NULL)
                OR (clarification_required = TRUE
                    AND detected_language IN ('so', 'en')
                    AND target_language IN ('so', 'en')
                    AND translated_text IS NULL
                    AND clarification_question IS NOT NULL)
                OR (detected_language = 'unsupported'
                    AND target_language IS NULL
                    AND interpreted_source_text IS NULL
                    AND translated_text IS NULL
                    AND clarification_required = FALSE
                    AND clarification_question IS NULL)
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
