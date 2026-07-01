-- Partial unique index on the live items master per household, so the Add path
-- can bump purchase stats with ON CONFLICT (household_id, lower(name)). Additive
-- and non-destructive (greenfield DB, no existing data).
CREATE UNIQUE INDEX items_household_name ON items (household_id, lower(name)) WHERE deleted_at IS NULL;
