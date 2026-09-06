DROP TABLE IF EXISTS text_translations;

ALTER TABLE whatsapp_senders
    RENAME CONSTRAINT whatsapp_senders_role_paid_messages_usage_check
    TO whatsapp_senders_role_recruiter_usage_check;

ALTER TABLE whatsapp_senders
    RENAME CONSTRAINT whatsapp_senders_paid_messages_usage_check
    TO whatsapp_senders_recruiter_usage_check;

ALTER TABLE whatsapp_senders
    RENAME COLUMN paid_messages_used TO recruiter_audio_messages_used;

COMMENT ON COLUMN whatsapp_senders.access_status IS
    'DISABLED is terminal for recruiter accounts after their third accepted audio message.';
COMMENT ON COLUMN whatsapp_senders.recruiter_audio_messages_used IS
    'Lifetime count of accepted recruiter audio messages; the application disables a recruiter at three.';
