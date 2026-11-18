BEGIN;
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM assets WHERE storage = 'oss') THEN
        RAISE EXCEPTION 'Cannot remove OSS storage support while retained OSS assets exist';
    END IF;
END $$;
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_storage_check;
ALTER TABLE assets ADD CONSTRAINT assets_storage_check CHECK (storage IN ('local', 's3'));
COMMIT;
