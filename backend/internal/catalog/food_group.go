package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// eurofirFacet is the LanguaL/EuroFIR facet name carrying the food group.
const eurofirFacet = "A Gruppindelning EuroFIR"

type klassRecord struct {
	Nummer           int `json:"nummer"`
	Klassificeringar []struct {
		Fasett string `json:"fasett"`
		Namn   string `json:"namn"`
	} `json:"klassificeringar"`
}

// ParseKlassificeringar reads the Livsmedelsverket klassificeringar dump and
// returns nummer -> EuroFIR food group name (trimmed). Products without the
// "A Gruppindelning EuroFIR" facet are omitted.
func ParseKlassificeringar(r io.Reader) (map[int]string, error) {
	var recs []klassRecord
	if err := json.NewDecoder(r).Decode(&recs); err != nil {
		return nil, fmt.Errorf("decode klassificeringar json: %w", err)
	}
	out := make(map[int]string, len(recs))
	for _, rec := range recs {
		for _, f := range rec.Klassificeringar {
			if f.Fasett == eurofirFacet {
				if g := strings.TrimSpace(f.Namn); g != "" {
					out[rec.Nummer] = g
				}
				break
			}
		}
	}
	return out, nil
}

// foodGroupAisles maps Livsmedelsverket EuroFIR food groups to the v1 aisle
// taxonomy. Keys are trimmed group names exactly as they appear in the data.
// Alcohol groups (out of scope per AGENTS.md), dietary supplements, and a few
// generic catch-all groups are intentionally omitted: such products get a
// food_group but fall back to the name heuristic for aisle.
var foodGroupAisles = map[string]int{
	// 1 produce
	"Grönsaker och svamp":              1,
	"Frukt och bär":                    1,
	"Grönsaksrätter":                   1,
	"Potatisrätter":                    1,
	"Grönsaksprodukter":                1,
	"Potatis och stärkelserika rötter": 1,
	"Grönsaker, rotfrukter och svamp":  1,
	"Färdigsallad":                     1,
	"Svamprätter":                      1,
	"Vegetariska produkter":            1,
	// 2 dairy & eggs
	"Fil och yoghurt":               2,
	"Vegetabiliska mejeriprodukter": 2,
	"Färskost":                      2,
	"Margarin och blandade fetter":  2,
	"Grädde":                        2,
	"Övriga ostprodukter":           2,
	"Ägg":                           2,
	"Äggrätter":                     2,
	"Mjukost":                       2,
	"Mjölk":                         2,
	"Hårdost":                       2,
	"Halvhård ost":                  2,
	"Mejeriprodukter":               2,
	"Övriga mjölkprodukter":         2,
	"Smör":                          2,
	"Extra hårdost":                 2,
	"Ost":                           2,
	// 3 meat
	"Rött kött":                   3,
	"Kötträtt":                    3,
	"Korv eller liknande produkt": 3,
	"Fågel":                       3,
	"Innanmat och inälvsmat":      3,
	"Kött eller köttprodukter":    3,
	// 4 fish & seafood
	"Fisk- och skaldjursrätt":    4,
	"Fisk":                       4,
	"Fisk- och skaldjursprodukt": 4,
	"Fisk och skaldjur":          4,
	// 5 bread & bakery
	"Bageriprodukter, söta och/eller feta": 5,
	"Ojäst bröd":                           5,
	"Jäst bröd":                            5,
	"Pannkaka eller våffla":                5,
	"Övriga bröd":                          5,
	"Smörgåsar":                            5,
	// 6 pantry
	"Sås i maträtt": 6,
	"Cerealierätter t.ex. klimp, risotto, pannkakor med fyllning, couscous, smörgåsar": 6,
	"Ris eller annat spannmål": 6,
	"Soppa":                    6,
	"Baljväxter":               6,
	"Processad frukt och bär":  6,
	"Matpaj eller pizza":       6,
	"Frukostflingor":           6,
	"Kryddning eller extrakt":  6,
	"Baljväxträtter":           6,
	"Cerealier eller cerealielika mjölprodukter och derivat": 6,
	"Sylt eller marmelad":                      6,
	"Pasta och liknande produkter":             6,
	"Pastarätter":                              6,
	"Smaksättare, tex ketchup, sojasås, senap": 6,
	"Vegetabiliskt fett och olja":              6,
	"Dressing, majonnäs":                       6,
	"Konserverat kött":                         6,
	"Krydda":                                   6,
	"Socker, honung eller sirap":               6,
	"Spannmål och spannmålsprodukter":          6,
	"Kryddor smaksättare dressing röror":       6,
	"Andra djurfetter":                         6,
	"Baknings ingrediens":                      6,
	"Socker och söta livsmedel":                6,
	"Chutney eller pickle":                     6,
	"Smakämne eller essencer":                  6,
	// 7 frozen
	"Glass och annan frusen dessert med mejeriprodukter": 7,
	// 8 beverages (non-alcoholic)
	"Dryck utan alkohol":  8,
	"Juice och nektar":    8,
	"Kaffe, te och kakao": 8,
	"Läsk":                8,
	"Vatten":              8,
	// 9 snacks & sweets
	"Choklad eller chokladprodukt": 9,
	"Dessert":                      9,
	"Konfekt och annan sockerprodukt dvs ej choklad": 9,
	"Snacks":                9,
	"Nöt eller frö produkt": 9,
	"Nöt, frö eller kärna":  9,
	"Dessertsås":            9,
	"Söta kakor":            9,
}

// FoodGroupAisle returns the v1 aisle for a EuroFIR food group, or nil when the
// group is unknown or intentionally unmapped (alcohol, supplements, generic
// catch-alls). The group is trimmed before lookup.
func FoodGroupAisle(group string) *int {
	if a, ok := foodGroupAisles[strings.TrimSpace(group)]; ok {
		return &a
	}
	return nil
}
