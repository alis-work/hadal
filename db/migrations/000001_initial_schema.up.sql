CREATE TABLE whatsapp_senders (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    phone_number TEXT NOT NULL UNIQUE,
    is_allowed BOOLEAN NOT NULL DEFAULT TRUE,
    daily_request_limit INTEGER NOT NULL CHECK (daily_request_limit > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX whatsapp_senders_allowed_phone_number_idx
    ON whatsapp_senders (phone_number)
    WHERE is_allowed;

CREATE TABLE daily_quota_usage (
    sender_id BIGINT NOT NULL REFERENCES whatsapp_senders (id) ON DELETE CASCADE,
    usage_date DATE NOT NULL,
    reserved_requests INTEGER NOT NULL DEFAULT 0 CHECK (reserved_requests >= 0),
    completed_requests INTEGER NOT NULL DEFAULT 0 CHECK (completed_requests >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (sender_id, usage_date)
);

COMMENT ON TABLE whatsapp_senders IS
    'Allowlist and daily paid-request policy for normalized WhatsApp sender phone numbers.';
COMMENT ON TABLE daily_quota_usage IS
    'Durable daily quota reservations and completed paid requests; Redis is only for short-window rate limiting.';
