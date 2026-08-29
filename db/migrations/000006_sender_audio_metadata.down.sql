DROP INDEX transcriptions_sender_id_idx;
ALTER TABLE transcriptions DROP CONSTRAINT transcriptions_sender_content_sha256_key;
ALTER TABLE transcriptions ADD CONSTRAINT transcriptions_content_sha256_key UNIQUE (content_sha256);
ALTER TABLE transcriptions DROP COLUMN audio_duration_seconds, DROP COLUMN sender_id;
