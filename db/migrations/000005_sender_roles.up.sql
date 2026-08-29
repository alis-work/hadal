ALTER TABLE whatsapp_senders
    ADD COLUMN role TEXT NOT NULL DEFAULT 'PERMITTED_USER',
    ADD COLUMN access_status TEXT NOT NULL DEFAULT 'ACTIVE',
    ADD COLUMN recruiter_audio_messages_used INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN disabled_at TIMESTAMPTZ;

UPDATE whatsapp_senders
SET role = 'ADMIN'
WHERE is_admin;

ALTER TABLE whatsapp_senders
    ADD CONSTRAINT whatsapp_senders_role_check
        CHECK (role IN ('ADMIN', 'PERMITTED_USER', 'RECRUITER')),
    ADD CONSTRAINT whatsapp_senders_access_status_check
        CHECK (access_status IN ('ACTIVE', 'DISABLED')),
    ADD CONSTRAINT whatsapp_senders_recruiter_usage_check
        CHECK (recruiter_audio_messages_used BETWEEN 0 AND 3),
    ADD CONSTRAINT whatsapp_senders_role_recruiter_usage_check
        CHECK (role = 'RECRUITER' OR recruiter_audio_messages_used = 0),
    ADD CONSTRAINT whatsapp_senders_disabled_at_check
        CHECK ((access_status = 'DISABLED') = (disabled_at IS NOT NULL));

COMMENT ON COLUMN whatsapp_senders.role IS
    'ADMIN is reserved for existing administrators; registration codes grant PERMITTED_USER or RECRUITER.';
COMMENT ON COLUMN whatsapp_senders.access_status IS
    'DISABLED is terminal for recruiter accounts after their third accepted audio message.';
COMMENT ON COLUMN whatsapp_senders.recruiter_audio_messages_used IS
    'Lifetime count of accepted recruiter audio messages; the application disables a recruiter at three.';
