DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM whatsapp_senders WHERE daily_request_limit IS NULL) THEN
        RAISE EXCEPTION 'Set a daily_request_limit for all senders before reverting this migration';
    END IF;
END $$;

ALTER TABLE whatsapp_senders
    DROP COLUMN is_admin;

ALTER TABLE whatsapp_senders
    DROP COLUMN country_code;

ALTER TABLE whatsapp_senders
    DROP CONSTRAINT whatsapp_senders_daily_request_limit_check;

ALTER TABLE whatsapp_senders
    ADD CONSTRAINT whatsapp_senders_daily_request_limit_check
    CHECK (daily_request_limit > 0);

ALTER TABLE whatsapp_senders
    ALTER COLUMN daily_request_limit SET NOT NULL;
