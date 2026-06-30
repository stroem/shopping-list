package sync

// entity is one syncable household-scoped table and its JSON projection. uuid
// columns are cast to text so generic rows marshal cleanly; household_id is
// omitted (the caller already knows its household).
type entity struct {
	name  string
	table string
	proj  string
}

// registry lists the household-scoped, *_sync-indexed tables (schema 0001_init).
// New entities (#9/#10/#11, stores) register here as they land.
var registry = []entity{
	{"lists", "lists", "id::text, name, archived_at, created_at, updated_at, deleted_at"},
	{"items", "items", "id::text, name, aisle, image_url, source, external_id, purchase_count, last_purchased_at, created_at, updated_at, deleted_at"},
	{"list_items", "list_items", "id::text, list_id::text, item_id::text, name, quantity, note, aisle, position, checked_at, checked_by::text, created_at, updated_at, deleted_at"},
	{"check_off_events", "check_off_events", "id::text, list_item_id::text, user_id::text, item_id::text, store_id::text, quantity, checked_at, created_at, updated_at, deleted_at"},
	{"users", "users", "id::text, display_name, created_at, updated_at, deleted_at"},
	{"stores", "stores", "id::text, name, chain, place_id, osm_id, latitude, longitude, address, created_at, updated_at, deleted_at"},
	{"store_aisles", "store_aisles", "id::text, store_id::text, aisle, position, label, created_at, updated_at, deleted_at"},
	{"store_items", "store_items", "id::text, store_id::text, item_id::text, aisle, position, available, last_seen_at, created_at, updated_at, deleted_at"},
}

// query is the per-entity since-cursor SELECT. household_id is parameterised and
// cast; updated_at > since uses the (household_id, updated_at) *_sync index.
func (e entity) query() string {
	return "SELECT " + e.proj + " FROM " + e.table +
		" WHERE household_id = $1::uuid AND updated_at > $2 ORDER BY updated_at"
}
