ALTER TABLE whatsapp_senders
    RENAME COLUMN recruiter_audio_messages_used TO paid_messages_used;

ALTER TABLE whatsapp_senders
    RENAME CONSTRAINT whatsapp_senders_recruiter_usage_check
    TO whatsapp_senders_paid_messages_usage_check;

ALTER TABLE whatsapp_senders
    RENAME CONSTRAINT whatsapp_senders_role_recruiter_usage_check
    TO whatsapp_senders_role_paid_messages_usage_check;

COMMENT ON COLUMN whatsapp_senders.access_status IS
    'DISABLED is terminal for recruiter accounts after their third accepted paid message.';
COMMENT ON COLUMN whatsapp_senders.paid_messages_used IS
    'Lifetime count of accepted recruiter paid messages, including text and audio; the application disables a recruiter at three.';

CREATE TABLE text_translations (
    id UUID PRIMARY KEY,
    inbound_message_id BIGINT NOT NULL UNIQUE
        REFERENCES whatsapp_inbound_messages (id) ON DELETE RESTRICT,
    sender_id BIGINT NOT NULL
        REFERENCES whatsapp_senders (id) ON DELETE RESTRICT,
    source_text TEXT NOT NULL
        CHECK (BTRIM(source_text) <> '' AND OCTET_LENGTH(source_text) <= 4096),
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')),
    detected_language TEXT,
    target_language TEXT,
    interpreted_source_text TEXT
        CHECK (interpreted_source_text IS NULL OR BTRIM(interpreted_source_text) <> ''),
    translated_text TEXT
        CHECK (translated_text IS NULL OR BTRIM(translated_text) <> ''),
    clarification_required BOOLEAN NOT NULL DEFAULT FALSE,
    clarification_question TEXT
        CHECK (clarification_question IS NULL OR BTRIM(clarification_question) <> ''),
    provider_attempt_count INTEGER NOT NULL DEFAULT 0
        CHECK (provider_attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ DEFAULT NOW(),
    failure_reason TEXT
        CHECK (failure_reason IS NULL OR BTRIM(failure_reason) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processing_started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    CONSTRAINT text_translations_language_pair_check CHECK (
        (detected_language IS NULL AND target_language IS NULL)
        OR (detected_language IS NOT NULL
            AND target_language IS NOT NULL
            AND detected_language IN ('so', 'en')
            AND target_language IN ('so', 'en')
            AND detected_language <> target_language)
    ),
    CONSTRAINT text_translations_state_result_check CHECK (
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
    )
);

CREATE INDEX text_translations_claim_idx
    ON text_translations (next_attempt_at, created_at)
    WHERE status = 'PENDING';

CREATE INDEX text_translations_processing_recovery_idx
    ON text_translations (processing_started_at)
    WHERE status = 'PROCESSING';

COMMENT ON TABLE text_translations IS
    'Durable asynchronous Somali-English text translation jobs and terminal results.';
