DROP INDEX IF EXISTS ean_mappings_name_trgm;

ALTER TABLE ean_mappings
    DROP COLUMN IF EXISTS quantity_text,
    DROP COLUMN IF EXISTS quantity_value,
    DROP COLUMN IF EXISTS quantity_unit,
    DROP COLUMN IF EXISTS serving_text,
    DROP COLUMN IF EXISTS serving_value,
    DROP COLUMN IF EXISTS nutriscore_grade,
    DROP COLUMN IF EXISTS nova_group,
    DROP COLUMN IF EXISTS nutriments,
    DROP COLUMN IF EXISTS ingredients,
    DROP COLUMN IF EXISTS allergens,
    DROP COLUMN IF EXISTS labels;
