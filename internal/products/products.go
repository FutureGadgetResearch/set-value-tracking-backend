package products

import (
	"encoding/json"
	"os"
)

type Product struct {
	SetID              string  `json:"set_id"`
	EnSetID            string  `json:"en_set_id"`  // corresponding EN set id; empty for EN sets
	TCG                string  `json:"tcg"`
	Era                string  `json:"era"`
	ReleaseDate        string  `json:"release_date"`
	IsSpecialSet       bool    `json:"is_special_set"`
	StandardLegalUntil string  `json:"standard_legal_until"` // "YYYY-MM-DD", empty means still legal
	ProductType        string  `json:"product_type"`
	MSRP               float64 `json:"msrp"`
	PacksPerProduct    int     `json:"packs_per_product"`
	PricechartingURL   string  `json:"pricecharting_url"`
	TCGPlayerID        string  `json:"tcgplayer_id"`
}

type productItem struct {
	ProductType      string  `json:"product_type"`
	MSRP             float64 `json:"msrp"`
	PacksPerProduct  int     `json:"packs_per_product"`
	PricechartingURL string  `json:"pricecharting_url"`
	TCGPlayerID      string  `json:"tcgplayer_id"`
}

type setData struct {
	EnSetID            string        `json:"en_set_id"`
	ReleaseDate        string        `json:"release_date"`
	IsSpecialSet       bool          `json:"is_special_set"`
	StandardLegalUntil string        `json:"standard_legal_until"`
	Products           []productItem `json:"products"`
}

// nestedFile mirrors the JSON layout: tcg -> era -> set_id -> setData
type nestedFile map[string]map[string]map[string]setData

func Load(path string) ([]Product, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var nested nestedFile
	if err := json.NewDecoder(f).Decode(&nested); err != nil {
		return nil, err
	}

	var products []Product
	for tcg, eras := range nested {
		for era, sets := range eras {
			for setID, sd := range sets {
				for _, item := range sd.Products {
					products = append(products, Product{
						SetID:              setID,
						EnSetID:            sd.EnSetID,
						TCG:                tcg,
						Era:                era,
						ReleaseDate:        sd.ReleaseDate,
						IsSpecialSet:       sd.IsSpecialSet,
						StandardLegalUntil: sd.StandardLegalUntil,
						ProductType:        item.ProductType,
						MSRP:               item.MSRP,
						PacksPerProduct:    item.PacksPerProduct,
						PricechartingURL:   item.PricechartingURL,
						TCGPlayerID:        item.TCGPlayerID,
					})
				}
			}
		}
	}
	return products, nil
}
