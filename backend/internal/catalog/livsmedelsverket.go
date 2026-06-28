package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type livsmedelEnvelope struct {
	Livsmedel []struct {
		Nummer int    `json:"nummer"`
		Namn   string `json:"namn"`
	} `json:"livsmedel"`
}

// ParseLivsmedelsverket decodes the Livsmedelsverket products JSON into Rows.
// Items with an empty name are skipped; aisle is derived from the name.
func ParseLivsmedelsverket(r io.Reader) ([]Row, error) {
	var env livsmedelEnvelope
	if err := json.NewDecoder(r).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode livsmedelsverket json: %w", err)
	}
	rows := make([]Row, 0, len(env.Livsmedel))
	for _, item := range env.Livsmedel {
		name := strings.TrimSpace(item.Namn)
		if name == "" {
			continue
		}
		rows = append(rows, Row{
			Source:     "livsmedelsverket",
			ExternalID: strconv.Itoa(item.Nummer),
			Name:       name,
			Aisle:      AisleFor(name),
		})
	}
	return rows, nil
}
