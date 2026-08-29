ALTER TABLE transcriptions
    ADD COLUMN sender_id BIGINT REFERENCES whatsapp_senders (id) ON DELETE RESTRICT,
    ADD COLUMN audio_duration_seconds DOUBLE PRECISION CHECK (audio_duration_seconds > 0);

ALTER TABLE transcriptions DROP CONSTRAINT transcriptions_content_sha256_key;
ALTER TABLE transcriptions ADD CONSTRAINT transcriptions_sender_content_sha256_key UNIQUE (sender_id, content_sha256);

CREATE INDEX transcriptions_sender_id_idx ON transcriptions (sender_id);

COMMENT ON COLUMN transcriptions.sender_id IS 'Registered sender that submitted this audio. Legacy records may have no sender.';
COMMENT ON COLUMN transcriptions.audio_duration_seconds IS 'Duration measured by ffprobe before queueing.';
