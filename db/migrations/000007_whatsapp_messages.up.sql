CREATE TABLE global_quota_usage (
    usage_date DATE PRIMARY KEY,
    reserved_requests INTEGER NOT NULL DEFAULT 0 CHECK (reserved_requests >= 0),
    completed_requests INTEGER NOT NULL DEFAULT 0 CHECK (completed_requests >= 0 AND completed_requests <= reserved_requests),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE transcriptions
    ADD COLUMN provider_attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (provider_attempt_count >= 0),
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE TABLE whatsapp_inbound_messages (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    whatsapp_message_id TEXT NOT NULL UNIQUE CHECK (BTRIM(whatsapp_message_id) <> ''),
    sender_phone TEXT NOT NULL CHECK (sender_phone ~ '^\+[1-9][0-9]{7,14}$'),
    message_type TEXT NOT NULL CHECK (message_type IN ('text', 'registration_permitted', 'registration_recruiter', 'registration_invalid', 'audio', 'image', 'video', 'document', 'sticker', 'unsupported')),
    message_text TEXT,
    media_id TEXT,
    media_content_type TEXT,
    media_filename TEXT,
    media_byte_size BIGINT CHECK (media_byte_size > 0),
    media_sha256 TEXT CHECK (media_sha256 ~ '^[0-9a-f]{64}$'),
    media_storage_path TEXT,
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ DEFAULT NOW(),
    transcription_id UUID REFERENCES transcriptions (id) ON DELETE RESTRICT,
    failure_reason TEXT CHECK (failure_reason IS NULL OR BTRIM(failure_reason) <> ''),
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processing_started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (message_type = 'text'
            AND message_text IS NOT NULL
            AND BTRIM(message_text) <> ''
            AND media_id IS NULL
            AND media_content_type IS NULL
            AND media_filename IS NULL
            AND media_byte_size IS NULL
            AND media_sha256 IS NULL
            AND media_storage_path IS NULL
            AND transcription_id IS NULL)
        OR (message_type IN ('registration_permitted', 'registration_recruiter', 'registration_invalid')
            AND message_text IS NULL
            AND media_id IS NULL)
        OR (message_type IN ('audio', 'image', 'video', 'document', 'sticker')
            AND message_text IS NULL
            AND media_id IS NOT NULL
            AND BTRIM(media_id) <> '')
        OR (message_type = 'unsupported'
            AND message_text IS NULL
            AND media_id IS NULL)
    ),
    CHECK (status <> 'PROCESSING' OR (attempt_count > 0 AND processing_started_at IS NOT NULL)),
    CHECK (status <> 'COMPLETED' OR (completed_at IS NOT NULL AND failure_reason IS NULL)),
    CHECK (status <> 'FAILED' OR (completed_at IS NULL AND failure_reason IS NOT NULL)),
    CHECK (transcription_id IS NULL OR message_type = 'audio'),
    CHECK (status IN ('PENDING', 'FAILED') OR next_attempt_at IS NULL)
);

CREATE INDEX whatsapp_inbound_messages_claim_idx
    ON whatsapp_inbound_messages (next_attempt_at, created_at)
    WHERE status IN ('PENDING', 'FAILED') AND next_attempt_at IS NOT NULL;

CREATE INDEX whatsapp_inbound_messages_sender_received_idx
    ON whatsapp_inbound_messages (sender_phone, received_at DESC);

CREATE INDEX whatsapp_inbound_messages_transcription_idx
    ON whatsapp_inbound_messages (transcription_id)
    WHERE transcription_id IS NOT NULL;

CREATE TABLE whatsapp_outbound_messages (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_message_id TEXT UNIQUE
        REFERENCES whatsapp_inbound_messages (whatsapp_message_id) ON DELETE RESTRICT,
    recipient_phone TEXT NOT NULL CHECK (recipient_phone ~ '^\+[1-9][0-9]{7,14}$'),
    reply_to_whatsapp_message_id TEXT CHECK (
        reply_to_whatsapp_message_id IS NULL OR BTRIM(reply_to_whatsapp_message_id) <> ''
    ),
    body TEXT NOT NULL CHECK (BTRIM(body) <> ''),
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'SENDING', 'SENT', 'FAILED', 'UNKNOWN')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ DEFAULT NOW(),
    provider_message_id TEXT UNIQUE,
    last_error TEXT CHECK (last_error IS NULL OR BTRIM(last_error) <> ''),
    sending_started_at TIMESTAMPTZ,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (status <> 'SENDING' OR (attempt_count > 0 AND sending_started_at IS NOT NULL)),
    CHECK (
        status <> 'SENT'
        OR (provider_message_id IS NOT NULL AND sent_at IS NOT NULL AND last_error IS NULL AND next_attempt_at IS NULL)
    ),
    CHECK (status <> 'FAILED' OR (attempt_count > 0 AND sent_at IS NULL AND last_error IS NOT NULL)),
    CHECK (status <> 'UNKNOWN' OR (attempt_count > 0 AND sent_at IS NULL AND last_error IS NOT NULL)),
    CHECK (status IN ('PENDING', 'FAILED') OR next_attempt_at IS NULL)
);

CREATE INDEX whatsapp_outbound_messages_claim_idx
    ON whatsapp_outbound_messages (next_attempt_at, created_at)
    WHERE status IN ('PENDING', 'FAILED') AND next_attempt_at IS NOT NULL;

COMMENT ON TABLE whatsapp_inbound_messages IS
    'Durable, idempotent WhatsApp intake and processing state; media bytes remain outside PostgreSQL.';
COMMENT ON TABLE whatsapp_outbound_messages IS
    'Transactional outbox for retryable WhatsApp replies.';
COMMENT ON COLUMN whatsapp_outbound_messages.source_message_id IS
    'Inbound WhatsApp provider message ID; uniqueness makes one result reply idempotent.';
