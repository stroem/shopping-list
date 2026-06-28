-- Open Food Facts product detail (issue #6). Normalized, fixed-shape columns;
-- the four jsonb columns are NOT NULL with empty defaults so the app detail page
-- never receives null. `raw` (from 0001) stays unused.
ALTER TABLE ean_mappings
    ADD COLUMN quantity_text    text,
    ADD COLUMN quantity_value   numeric,
    ADD COLUMN quantity_unit    text,
    ADD COLUMN serving_text     text,
    ADD COLUMN serving_value    numeric,
    ADD COLUMN nutriscore_grade text,
    ADD COLUMN nova_group       int,
    ADD COLUMN nutriments  jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN ingredients jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN allergens   jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN labels      jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Branded-product autocomplete: trigram search over the ~23k OFF names,
-- unioned with food_catalog by the #7 endpoint.
CREATE INDEX ean_mappings_name_trgm ON ean_mappings USING gin (name gin_trgm_ops);
