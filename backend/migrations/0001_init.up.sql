-- v1 schema. Greenfield. All identifiers English.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE households (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE TABLE users (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    text NOT NULL UNIQUE,
    display_name text,
    household_id uuid REFERENCES households(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

CREATE TABLE lists (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    name         text NOT NULL,
    archived_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

CREATE TABLE items (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id      uuid NOT NULL REFERENCES households(id),
    name              text NOT NULL,
    aisle             int,
    image_url         text,
    source            text,
    external_id       text,
    purchase_count    int NOT NULL DEFAULT 0,
    last_purchased_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);

CREATE TABLE list_items (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    list_id      uuid NOT NULL REFERENCES lists(id),
    item_id      uuid REFERENCES items(id),
    name         text NOT NULL,
    quantity     int NOT NULL DEFAULT 1,
    note         text,
    aisle        int,
    position     int NOT NULL DEFAULT 0,
    checked_at   timestamptz,
    checked_by   uuid REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

CREATE TABLE stores (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    name         text NOT NULL,
    chain        text,
    place_id     text,
    osm_id       text,
    latitude     double precision,
    longitude    double precision,
    address      text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    UNIQUE (household_id, place_id)
);

CREATE TABLE store_aisles (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    store_id     uuid NOT NULL REFERENCES stores(id),
    aisle        int NOT NULL,
    position     int NOT NULL DEFAULT 0,
    label        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    UNIQUE (store_id, aisle)
);

CREATE TABLE store_items (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    store_id     uuid NOT NULL REFERENCES stores(id),
    item_id      uuid NOT NULL REFERENCES items(id),
    aisle        int,
    position     int,
    available    boolean NOT NULL DEFAULT true,
    last_seen_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    UNIQUE (store_id, item_id)
);

CREATE TABLE check_off_events (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    list_item_id uuid REFERENCES list_items(id),
    user_id      uuid REFERENCES users(id),
    item_id      uuid REFERENCES items(id),
    store_id     uuid REFERENCES stores(id),
    quantity     int NOT NULL DEFAULT 1,
    checked_at   timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

CREATE TABLE food_catalog (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source      text NOT NULL,
    external_id text,
    name        text NOT NULL,
    food_group  text,
    aisle       int,
    image_url   text,
    raw         jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    UNIQUE (source, external_id)
);

CREATE TABLE ean_mappings (
    ean        text PRIMARY KEY,
    name       text NOT NULL,
    brand      text,
    aisle      int,
    image_url  text,
    source     text NOT NULL,
    raw        jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

-- Indexes: autocomplete
CREATE INDEX items_name_trgm        ON items        USING gin (name gin_trgm_ops);
CREATE INDEX food_catalog_name_trgm ON food_catalog USING gin (name gin_trgm_ops);
CREATE INDEX items_autocomplete     ON items (household_id, purchase_count DESC, last_purchased_at DESC);

-- Indexes: pull-sync cursor + household scoping
CREATE INDEX lists_sync            ON lists            (household_id, updated_at);
CREATE INDEX items_sync            ON items            (household_id, updated_at);
CREATE INDEX list_items_sync       ON list_items       (household_id, updated_at);
CREATE INDEX check_off_events_sync ON check_off_events (household_id, updated_at);
CREATE INDEX users_sync            ON users            (household_id, updated_at);
CREATE INDEX stores_sync           ON stores           (household_id, updated_at);
CREATE INDEX store_aisles_sync     ON store_aisles     (household_id, updated_at);
CREATE INDEX store_items_sync      ON store_items      (household_id, updated_at);

-- Indexes: lookups
CREATE INDEX list_items_list        ON list_items       (list_id);
CREATE INDEX check_off_events_store ON check_off_events (store_id, item_id);
