package catalog

// Row is one food_catalog record to upsert.
type Row struct {
	Source     string
	ExternalID string
	Name       string
	Aisle      *int
}
