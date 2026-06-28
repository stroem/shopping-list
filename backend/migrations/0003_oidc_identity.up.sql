-- A user is now a person keyed by their OIDC identity (issuer, subject).
-- device_id is no longer the identity (kept nullable for future device/sync use).
ALTER TABLE users
    ADD COLUMN issuer  text,
    ADD COLUMN subject text,
    ADD COLUMN email   text,
    ALTER COLUMN device_id DROP NOT NULL;
CREATE UNIQUE INDEX users_issuer_subject ON users (issuer, subject);

-- Households are shared via an unguessable invite code (carried by the link).
ALTER TABLE households
    ADD COLUMN invite_code text NOT NULL DEFAULT gen_random_uuid()::text;
CREATE UNIQUE INDEX households_invite_code ON households (invite_code);
