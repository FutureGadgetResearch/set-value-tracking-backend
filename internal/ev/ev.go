package ev

import (
	"encoding/json"
	"os"
	"sort"
	"time"

	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/setdata"
)

// GradedPrices holds prices for graded copies of a card. All fields are
// pointers so that absent data serialises as omitted rather than zero.
//
// PSA10 and Grade9 are populated for every month from historical chart data.
// TAG10 through CGC10Pristine are populated for the most recent month only,
// scraped from each grading company's individual PriceCharting sub-page.
type GradedPrices struct {
	PSA10           *float64 `json:"psa_10,omitempty"`
	Grade9          *float64 `json:"grade_9,omitempty"`
	TAG10           *float64 `json:"tag_10,omitempty"`
	ACE10           *float64 `json:"ace_10,omitempty"`
	SGC10           *float64 `json:"sgc_10,omitempty"`
	CGC10           *float64 `json:"cgc_10,omitempty"`
	BGS10           *float64 `json:"bgs_10,omitempty"`
	BGS10BlackLabel *float64 `json:"bgs_10_black_label,omitempty"`
	CGC10Pristine   *float64 `json:"cgc_10_pristine,omitempty"`
}

// CardPrice is the price of one card in a specific month.
// GradedPrices is only set for illustration_rare, special_illustration_rare,
// and hyper_rare cards.
type CardPrice struct {
	Number       string        `json:"number"`
	Name         string        `json:"name"`
	Rarity       string        `json:"rarity"`
	PriceUSD     float64       `json:"price_usd"`
	GradedPrices *GradedPrices `json:"graded_prices,omitempty"`
}

// MonthEV holds all computed metrics plus raw card prices for one calendar month.
type MonthEV struct {
	Month      string      `json:"month"` // "2023-03"
	CardPrices []CardPrice `json:"card_prices"`
	EV         float64     `json:"ev"`
	SetValue   float64     `json:"set_value"`
	Top5Value  float64     `json:"top_5_value"`
	Top5Ratio  float64     `json:"top_5_ratio"`
}

// History is the persisted cache of monthly EV data for a set.
type History struct {
	SetID  string    `json:"set_id"`
	Months []MonthEV `json:"months"`
}

// Calculate computes EV, set value, and top-5 value from a set of card prices
// for a single month. Cards not present in cardPrices are excluded from
// their rarity's average (i.e., only priced cards contribute).
// EV is calculated from ungraded prices only.
func Calculate(pr *setdata.PullRates, cardPrices []CardPrice) MonthEV {
	// Group ungraded prices by rarity.
	byRarity := make(map[string][]float64, len(pr.Rarities))
	var setValue float64
	for _, cp := range cardPrices {
		byRarity[cp.Rarity] = append(byRarity[cp.Rarity], cp.PriceUSD)
		setValue += cp.PriceUSD
	}

	// EV: for each rarity, expected_hits_per_box × avg_card_price_in_rarity.
	var totalEV float64
	for _, rate := range pr.Rarities {
		prices := byRarity[rate.Rarity]
		if len(prices) == 0 {
			continue
		}
		var sum float64
		for _, p := range prices {
			sum += p
		}
		avgPrice := sum / float64(len(prices))
		totalEV += rate.PullRatePerPack * avgPrice
	}

	// Top-5 value.
	allPrices := make([]float64, len(cardPrices))
	for i, cp := range cardPrices {
		allPrices[i] = cp.PriceUSD
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(allPrices)))

	var top5Value float64
	for i := 0; i < 5 && i < len(allPrices); i++ {
		top5Value += allPrices[i]
	}

	var top5Ratio float64
	if setValue > 0 {
		top5Ratio = top5Value / setValue
	}

	return MonthEV{
		CardPrices: cardPrices,
		EV:         totalEV,
		SetValue:   setValue,
		Top5Value:  top5Value,
		Top5Ratio:  top5Ratio,
	}
}

// LoadHistory loads the EV history from disk. Returns an empty History (no
// error) if the file does not exist yet.
func LoadHistory(path string) (*History, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &History{}, nil
		}
		return nil, err
	}
	defer f.Close()
	var h History
	if err := json.NewDecoder(f).Decode(&h); err != nil {
		return nil, err
	}
	return &h, nil
}

// SaveHistory writes the EV history to disk as indented JSON.
func SaveHistory(path string, h *History) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(h)
}

// Lookup returns the MonthEV for the calendar month containing t.
func (h *History) Lookup(t time.Time) (MonthEV, bool) {
	key := t.Format("2006-01")
	for _, m := range h.Months {
		if m.Month == key {
			return m, true
		}
	}
	return MonthEV{}, false
}

// HasMonth reports whether the history already contains an entry for the given
// "YYYY-MM" month key.
func (h *History) HasMonth(month string) bool {
	for _, m := range h.Months {
		if m.Month == month {
			return true
		}
	}
	return false
}

// Upsert inserts or replaces the MonthEV entry for its month, keeping the
// slice sorted chronologically.
func (h *History) Upsert(m MonthEV) {
	for i, existing := range h.Months {
		if existing.Month == m.Month {
			h.Months[i] = m
			return
		}
	}
	h.Months = append(h.Months, m)
	sort.Slice(h.Months, func(i, j int) bool {
		return h.Months[i].Month < h.Months[j].Month
	})
}
