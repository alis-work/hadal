ALTER TABLE whatsapp_senders
    ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE whatsapp_senders
    ADD COLUMN country_code TEXT NOT NULL
    CHECK (country_code ~ '^\+[1-9][0-9]{0,2}$');

ALTER TABLE whatsapp_senders
    ALTER COLUMN daily_request_limit DROP NOT NULL;

ALTER TABLE whatsapp_senders
    DROP CONSTRAINT whatsapp_senders_daily_request_limit_check;

ALTER TABLE whatsapp_senders
    ADD CONSTRAINT whatsapp_senders_daily_request_limit_check
    CHECK (daily_request_limit IS NULL OR daily_request_limit > 0);

COMMENT ON COLUMN whatsapp_senders.is_admin IS
    'Administrative sender flag. It does not bypass authorization unless the application explicitly defines that behavior.';
COMMENT ON COLUMN whatsapp_senders.country_code IS
    'International calling code stored separately from the normalized full phone number.';
COMMENT ON COLUMN whatsapp_senders.daily_request_limit IS
    'Maximum paid requests per UTC day; NULL means no per-sender daily limit.';
