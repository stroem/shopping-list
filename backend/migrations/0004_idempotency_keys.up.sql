CREATE TABLE idempotency_keys (
    household_id  uuid NOT NULL REFERENCES households(id),
    key           text NOT NULL,
    method        text NOT NULL,
    path          text NOT NULL,
    status_code   int  NOT NULL,
    response_body bytea NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (household_id, key)
);
