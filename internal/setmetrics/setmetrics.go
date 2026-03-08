package setmetrics

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// Entry holds the EV metrics for one set in one calendar month.
type Entry struct {
	SetID     string  `json:"set_id"`
	Month     string  `json:"month"` // "YYYY-MM-DD" as stored in JSON
	EV        float64 `json:"ev"`
	SetValue  float64 `json:"set_value"`
	Top5Value float64 `json:"top_5_value"`
	Top5Ratio float64 `json:"top_5_ratio"`
}

// Metrics is an in-memory index of set metrics, keyed by set_id → "YYYY-MM".
type Metrics struct {
	index map[string]map[string]Entry
}

type rawResponse struct {
	Data []Entry `json:"data"`
}

// load decodes the set_metrics JSON from r and returns an indexed Metrics.
func load(r io.Reader) (*Metrics, error) {
	var raw rawResponse
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("setmetrics decode: %w", err)
	}
	m := &Metrics{index: make(map[string]map[string]Entry, len(raw.Data))}
	for _, e := range raw.Data {
		// Normalise month key to "YYYY-MM" regardless of whether the JSON
		// stores "YYYY-MM-DD" or "YYYY-MM".
		key := e.Month
		if len(key) > 7 {
			key = key[:7]
		}
		if m.index[e.SetID] == nil {
			m.index[e.SetID] = make(map[string]Entry)
		}
		m.index[e.SetID][key] = e
	}
	return m, nil
}

// LoadFromFile reads set_metrics from a local JSON file.
func LoadFromFile(path string) (*Metrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("setmetrics open %s: %w", path, err)
	}
	defer f.Close()
	return load(f)
}

// LoadFromURL fetches set_metrics from an HTTP(S) endpoint.
func LoadFromURL(url string) (*Metrics, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("setmetrics fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("setmetrics fetch %s: HTTP %d", url, resp.StatusCode)
	}
	return load(resp.Body)
}

// Lookup returns the metrics entry for (setID, month) where month is "YYYY-MM".
func (m *Metrics) Lookup(setID, month string) (Entry, bool) {
	bySet, ok := m.index[setID]
	if !ok {
		return Entry{}, false
	}
	e, ok := bySet[month]
	return e, ok
}
