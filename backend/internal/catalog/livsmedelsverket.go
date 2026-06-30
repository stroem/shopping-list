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
// Items with an empty name are skipped. When groups (nummer -> EuroFIR food
// group, from ParseKlassificeringar) is non-nil, each row's FoodGroup is set and
// its aisle prefers the food-group mapping, falling back to the name heuristic.
func ParseLivsmedelsverket(r io.Reader, groups map[int]string) ([]Row, error) {
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
		row := Row{
			Source:     "livsmedelsverket",
			ExternalID: strconv.Itoa(item.Nummer),
			Name:       name,
		}
		if g, ok := groups[item.Nummer]; ok {
			group := g
			row.FoodGroup = &group
			row.Aisle = FoodGroupAisle(g)
		}
		if row.Aisle == nil {
			row.Aisle = AisleFor(name)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
