-- Recipes and their ingredients. Household-scoped, owner-tagged, with private/
-- household visibility. Tables only here; indexes land in a later migration.
CREATE TABLE recipes (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id    uuid NOT NULL REFERENCES households(id),
    owner_device_id text NOT NULL,
    visibility      text NOT NULL DEFAULT 'private' CHECK (visibility IN ('household','private')),
    title           text NOT NULL,
    source_url      text,
    source_type     text CHECK (source_type IN ('website','youtube','tiktok')),
    image_url       text,
    servings        int,
    steps           jsonb NOT NULL DEFAULT '[]',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE TABLE recipe_ingredients (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    recipe_id  uuid NOT NULL REFERENCES recipes(id),
    position   int NOT NULL DEFAULT 0,
    raw_text   text,
    name       text NOT NULL,
    amount     numeric,
    unit       text,
    catalog_id uuid REFERENCES food_catalog(id),
    aisle      int,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
