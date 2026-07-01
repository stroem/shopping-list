-- Carry the client-generated event id on check_off_events so outbox replays and
-- double-taps dedup instead of appending a second event. Additive and reversible;
-- the partial unique index scopes dedup to live rows per household.
ALTER TABLE check_off_events ADD COLUMN client_event_id uuid;
CREATE UNIQUE INDEX check_off_events_client_event
  ON check_off_events (household_id, client_event_id)
  WHERE client_event_id IS NOT NULL AND deleted_at IS NULL;
