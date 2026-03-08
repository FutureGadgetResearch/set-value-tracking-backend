package pricecharting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var soldDateRe = regexp.MustCompile(`<td class="date">(\d{4}-\d{2}-\d{2})</td>`)

// fullPricesTableRe isolates the table inside the <div id="full-prices"> block.
// The id is on the wrapping div, not the table itself.
var fullPricesTableRe = regexp.MustCompile(`(?s)<div[^>]+id="full-prices"[^>]*>.*?<table[^>]*>(.*?)</table>`)

// fullPriceRowRe extracts (condition-name, price-string) pairs from within
// that table. Matches any <td> for the condition name followed by a .price <td>.
// The condition name cell has no class attribute on PriceCharting pages.
var fullPriceRowRe = regexp.MustCompile(`(?i)<td[^>]*>\s*([^<]+?)\s*</td>[\s\S]*?<td[^>]*class="[^"]*price[^"]*"[^>]*>\s*\$?([\d,]+\.?\d*)\s*</td>`)

// fullPriceKeyMap normalises the condition name strings PriceCharting uses in
// the Full Price Guide table to our internal key names. All lookups are done
// after strings.ToLower + strings.TrimSpace.
var fullPriceKeyMap = map[string]string{
	"ungraded":           "ungraded",
	"grade 9":            "grade_9",
	"psa 10":             "psa_10",
	"tag 10":             "tag_10",
	"tag gem mint 10":    "tag_10",
	"ace 10":             "ace_10",
	"ace gem mint 10":    "ace_10",
	"sgc 10":             "sgc_10",
	"cgc 10":             "cgc_10",
	"bgs 10":             "bgs_10",
	"bgs 10 black label": "bgs_10_black_label",
	"bgs 10 black":       "bgs_10_black_label",
	"cgc 10 pristine":    "cgc_10_pristine",
	"cgc pristine 10":    "cgc_10_pristine",
}

// ScrapeSoldLast30Days counts how many sold listings appear on the PriceCharting
// page within the last 30 days. If the visible data spans fewer than 30 days it
// extrapolates: (count / span_days) * 30.
func ScrapeSoldLast30Days(url string) (float64, error) {
	body, err := fetchPage(url)
	if err != nil {
		return 0, err
	}

	matches := soldDateRe.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return 0, fmt.Errorf("no sold listing dates found on page")
	}

	now := time.Now().UTC().Truncate(24 * time.Hour)
	cutoff30 := now.AddDate(0, 0, -30)

	var dates []time.Time
	for _, m := range matches {
		t, err := time.Parse("2006-01-02", string(m[1]))
		if err != nil {
			continue
		}
		dates = append(dates, t)
	}
	if len(dates) == 0 {
		return 0, fmt.Errorf("no valid dates parsed from sold listings")
	}

	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	oldest, newest := dates[0], dates[len(dates)-1]
	spanDays := newest.Sub(oldest).Hours()/24 + 1

	if spanDays >= 30 {
		count := 0
		for _, d := range dates {
			if !d.Before(cutoff30) {
				count++
			}
		}
		return float64(count), nil
	}

	// Fewer than 30 days of data — extrapolate.
	return math.Round(float64(len(dates)) / spanDays * 30), nil
}

// MonthlyPrice is the market price of a product on the 15th of a given month.
type MonthlyPrice struct {
	SnapshotDate time.Time
	PriceUSD     float64
}

// Scrape fetches the PriceCharting page and returns one price entry per
// calendar month from the product's history, each dated the 15th of that month.
// For sealed products the "new" condition is preferred, falling back to
// "boxonly" then "used". For individual cards "used" is the ungraded price.
func Scrape(url string) ([]MonthlyPrice, error) {
	body, err := fetchPage(url)
	if err != nil {
		return nil, err
	}

	raw, err := extractChartData(body)
	if err != nil {
		return nil, err
	}

	// PriceCharting stores the "ungraded" (main sealed) price under "used".
	// "new" and "boxonly" are populated for video games, not Pokemon sealed.
	for _, key := range []string{"used", "new", "boxonly"} {
		if entries, ok := raw[key]; ok && len(entries) > 0 {
			return aggregateByMonth(entries), nil
		}
	}
	return nil, fmt.Errorf("no usable price data found in chart")
}

// CardGradedHistory holds monthly price histories for a card's ungraded price
// plus PSA 10 and Grade 9, along with the current prices from the Full Price
// Guide table at the bottom of the page.
//
// All data comes from a single HTTP request to the base card page.
// chart_data keys used:
//   - "used"       → ungraded monthly history
//   - "manualonly" → PSA 10 monthly history (manually curated PSA 10 prices)
//   - "graded"     → Grade 9 monthly history
//
// Verified against the Full Price Guide table: the most recent "manualonly"
// value matches the table's PSA 10 price and "graded" matches Grade 9.
//
// CurrentGuide keys (from the Full Price Guide table) match the internal key
// names used in ev.GradedPrices JSON tags, e.g. "psa_10", "sgc_10".
type CardGradedHistory struct {
	Ungraded     []MonthlyPrice
	PSA10        []MonthlyPrice
	Grade9       []MonthlyPrice
	CurrentGuide map[string]float64 // condition key → current price (USD)
}

// ScrapeCardGradedHistory fetches one PriceCharting page and returns monthly
// histories for ungraded, PSA 10, and Grade 9 conditions, plus the current
// prices for all conditions listed in the Full Price Guide table — all from a
// single HTTP request.
//
// Key mapping: "used" → ungraded, "manualonly" → PSA 10, "graded" → Grade 9.
func ScrapeCardGradedHistory(url string) (CardGradedHistory, error) {
	body, err := fetchPage(url)
	if err != nil {
		return CardGradedHistory{}, err
	}

	raw, err := extractChartData(body)
	if err != nil {
		return CardGradedHistory{}, err
	}

	var h CardGradedHistory
	if entries, ok := raw["used"]; ok && len(entries) > 0 {
		h.Ungraded = aggregateByMonth(entries)
	}
	if entries, ok := raw["manualonly"]; ok && len(entries) > 0 {
		h.PSA10 = aggregateByMonth(entries)
	}
	if entries, ok := raw["graded"]; ok && len(entries) > 0 {
		h.Grade9 = aggregateByMonth(entries)
	}
	h.CurrentGuide = extractFullPriceGuide(body)
	return h, nil
}

// extractFullPriceGuide parses the "Full Price Guide" table from PriceCharting
// page HTML and returns a map of internal key → current price in USD.
// Returns an empty map (no error) if the section is absent.
func extractFullPriceGuide(body []byte) map[string]float64 {
	result := make(map[string]float64)

	tableMatch := fullPricesTableRe.FindSubmatch(body)
	if tableMatch == nil {
		return result
	}

	rowMatches := fullPriceRowRe.FindAllSubmatch(tableMatch[1], -1)
	for _, m := range rowMatches {
		conditionName := strings.ToLower(strings.TrimSpace(string(m[1])))
		priceStr := strings.ReplaceAll(string(m[2]), ",", "")
		price, err := strconv.ParseFloat(priceStr, 64)
		if err != nil || price == 0 {
			continue
		}
		if key, ok := fullPriceKeyMap[conditionName]; ok {
			result[key] = price
		}
	}
	return result
}

// fetchPage fetches the URL, automatically retrying on 429 responses by
// honouring the Retry-After header (defaulting to 60 s if absent).
func fetchPage(url string) ([]byte, error) {
	const maxRetries = 5

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetching %s: %w", url, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			wait := retryAfterDuration(resp)
			resp.Body.Close()
			log.Printf("  %d rate-limited; waiting %s before retry (attempt %d/%d)",
				resp.StatusCode, wait.Round(time.Second), attempt, maxRetries)
			time.Sleep(wait)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
		}
		if readErr != nil {
			return nil, fmt.Errorf("reading body from %s: %w", url, readErr)
		}
		return body, nil
	}

	return nil, fmt.Errorf("gave up after %d attempts (persistent 429) on %s", maxRetries, url)
}

// retryAfterDuration reads the Retry-After response header and returns how long
// to wait. Falls back to 60 s if the header is missing or unparseable.
func retryAfterDuration(resp *http.Response) time.Duration {
	const defaultWait = 60 * time.Second
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return defaultWait
	}
	// Integer seconds.
	if secs, err := strconv.Atoi(ra); err == nil {
		return time.Duration(secs) * time.Second
	}
	// HTTP date.
	if t, err := http.ParseTime(ra); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return defaultWait
}

// extractChartData finds VGPC.chart_data = {...} in the page HTML and parses
// it into a map of condition name → [[timestampMs, priceCents], ...].
func extractChartData(body []byte) (map[string][][]float64, error) {
	marker := []byte("VGPC.chart_data = ")
	idx := bytes.Index(body, marker)
	if idx == -1 {
		return nil, fmt.Errorf("VGPC.chart_data not found on page")
	}
	start := idx + len(marker)

	// Walk forward counting braces to find the matching closing brace.
	depth, end := 0, -1
	for i := start; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end > 0 {
			break
		}
	}
	if end == -1 {
		return nil, fmt.Errorf("could not find end of VGPC.chart_data JSON")
	}

	var raw map[string][][]float64
	if err := json.Unmarshal(body[start:end], &raw); err != nil {
		return nil, fmt.Errorf("parsing chart data JSON: %w", err)
	}
	return raw, nil
}

// aggregateByMonth groups raw [timestampMs, priceCents] entries by calendar
// month and picks the entry with a date closest to the 15th of that month.
// Entries with a zero price are skipped.
func aggregateByMonth(entries [][]float64) []MonthlyPrice {
	type entry struct {
		t     time.Time
		cents float64
	}

	var parsed []entry
	for _, e := range entries {
		if len(e) < 2 || e[1] == 0 {
			continue
		}
		t := time.Unix(int64(e[0])/1000, 0).UTC()
		parsed = append(parsed, entry{t: t, cents: e[1]})
	}

	type monthKey struct{ year, month int }
	byMonth := make(map[monthKey][]entry)
	for _, e := range parsed {
		k := monthKey{e.t.Year(), int(e.t.Month())}
		byMonth[k] = append(byMonth[k], e)
	}

	results := make([]MonthlyPrice, 0, len(byMonth))
	for k, mes := range byMonth {
		target := time.Date(k.year, time.Month(k.month), 15, 0, 0, 0, 0, time.UTC)
		best := mes[0]
		bestDiff := absDuration(mes[0].t.Sub(target))
		for _, e := range mes[1:] {
			if d := absDuration(e.t.Sub(target)); d < bestDiff {
				bestDiff = d
				best = e
			}
		}
		results = append(results, MonthlyPrice{
			SnapshotDate: target,
			PriceUSD:     float64(int(best.cents+0.5)) / 100.0, // round to cents
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].SnapshotDate.Before(results[j].SnapshotDate)
	})
	return results
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
