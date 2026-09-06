DROP TABLE IF EXISTS whatsapp_outbound_messages;
DROP TABLE IF EXISTS whatsapp_inbound_messages;
DROP TABLE IF EXISTS global_quota_usage;
ALTER TABLE transcriptions
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS provider_attempt_count;
