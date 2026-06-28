DROP INDEX IF EXISTS households_invite_code;
ALTER TABLE households DROP COLUMN IF EXISTS invite_code;

DROP INDEX IF EXISTS users_issuer_subject;
ALTER TABLE users
    DROP COLUMN IF EXISTS email,
    DROP COLUMN IF EXISTS subject,
    DROP COLUMN IF EXISTS issuer,
    ALTER COLUMN device_id SET NOT NULL;
