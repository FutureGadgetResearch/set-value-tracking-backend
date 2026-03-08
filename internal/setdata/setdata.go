package setdata

import (
	"encoding/json"
	"fmt"
	"os"
)

type Card struct {
	Number           string `json:"number"`
	Name             string `json:"name"`
	Rarity           string `json:"rarity"`
	PricechartingURL string `json:"pricecharting_url"`
}

type SetContents struct {
	SetID string `json:"set_id"`
	Cards []Card `json:"cards"`
}

type RarityRate struct {
	Rarity          string  `json:"rarity"`
	PullRatePerPack float64 `json:"pull_rate_per_pack"`
}

type PullRates struct {
	SetID       string       `json:"set_id"`
	SetName     string       `json:"set_name"`
	PacksPerBox int          // not in JSON; populated by LoadAllPullRates (default 36)
	Rarities    []RarityRate `json:"rarities"`
}

// defaultPacksPerBox is used when packs_per_box is not present in the JSON.
// All mainline SV booster boxes contain 36 packs.
const defaultPacksPerBox = 36

// contentsFile is the nested JSON shape: era -> set_id -> { cards }
type contentsFile map[string]map[string]struct {
	Cards []Card `json:"cards"`
}

// LoadAllContents reads the nested set_contents.json and returns all entries as a flat slice.
func LoadAllContents(path string) ([]SetContents, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var nested contentsFile
	if err := json.NewDecoder(f).Decode(&nested); err != nil {
		return nil, err
	}
	var list []SetContents
	for _, sets := range nested {
		for setID, entry := range sets {
			list = append(list, SetContents{SetID: setID, Cards: entry.Cards})
		}
	}
	return list, nil
}

// LoadContents reads the nested set_contents.json and returns the entry matching setID.
func LoadContents(path, setID string) (*SetContents, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var nested contentsFile
	if err := json.NewDecoder(f).Decode(&nested); err != nil {
		return nil, err
	}
	for _, sets := range nested {
		if entry, ok := sets[setID]; ok {
			return &SetContents{SetID: setID, Cards: entry.Cards}, nil
		}
	}
	return nil, fmt.Errorf("set %q not found in %s", setID, path)
}

// pullRateEntry is the per-set shape inside the nested JSON file.
type pullRateEntry struct {
	SetName  string       `json:"set_name"`
	Rarities []RarityRate `json:"rarities"`
}

// LoadAllPullRates reads the consolidated set_pull_rates.json (era → set_id → entry)
// and returns a map of set_id → *PullRates.
// PacksPerBox defaults to defaultPacksPerBox (36) for every set.
func LoadAllPullRates(path string) (map[string]*PullRates, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// nested: era -> set_id -> pullRateEntry
	var nested map[string]map[string]pullRateEntry
	if err := json.NewDecoder(f).Decode(&nested); err != nil {
		return nil, err
	}

	m := make(map[string]*PullRates)
	for _, sets := range nested {
		for setID, entry := range sets {
			pr := &PullRates{
				SetID:       setID,
				SetName:     entry.SetName,
				PacksPerBox: defaultPacksPerBox,
				Rarities:    entry.Rarities,
			}
			m[setID] = pr
		}
	}
	return m, nil
}
