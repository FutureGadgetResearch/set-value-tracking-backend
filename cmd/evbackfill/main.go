// evbackfill scrapes monthly price history for every card in a set's
// contents.json, computes EV / set-value / top-5 for each calendar month,
// and writes the results to ev_history.json.
//
// For illustration_rare, special_illustration_rare, and hyper_rare cards it
// additionally captures — all from the same single HTTP request per card:
//   - PSA 10 and Grade 9 monthly histories (from VGPC.chart_data)
//   - Current prices for all graders listed in the Full Price Guide table
//     (TAG 10, ACE 10, SGC 10, CGC 10, BGS 10, BGS 10 Black Label,
//     CGC 10 Pristine, and any others PriceCharting tracks)
//
// Run once to populate historical data; re-run any time to refresh the
// current month or fill in gaps from failed scrapes.
//
// Usage (from repo root):
//
//	go run ./cmd/evbackfill
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/ev"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/gcs"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/pricecharting"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/setdata"
)

const (
	contentsPath  = "data/pokemon/set_contents.json"
	pullRatesPath = "data/pokemon/set_pull_rates.json"

	// politeDelay keeps PriceCharting from rate-limiting us between requests.
	politeDelay = 500 * time.Millisecond
)

// gradedRarities are the card rarities for which we track PSA 10, Grade 9,
// and current grading-company prices.
var gradedRarities = map[string]bool{
	"illustration_rare":         true,
	"special_illustration_rare": true,
	"hyper_rare":                true,
}

func main() {
	setFlag := flag.String("set", "", "set ID to process (e.g. sv02); omit to process all sets")
	flag.Parse()

	ctx := context.Background()
	var gcsClient *gcs.Client
	if bucket := os.Getenv("GCS_BUCKET"); bucket != "" {
		var err error
		gcsClient, err = gcs.NewClient(ctx, bucket)
		if err != nil {
			log.Fatalf("creating gcs client: %v", err)
		}
		if err := gcsClient.Download(ctx, "data/pokemon/set_contents.json", "data/pokemon/set_contents.json"); err != nil {
			log.Fatalf("gcs download set_contents.json: %v", err)
		}
		if err := gcsClient.Download(ctx, "data/pokemon/set_pull_rates.json", "data/pokemon/set_pull_rates.json"); err != nil {
			log.Fatalf("gcs download set_pull_rates.json: %v", err)
		}
		if err := gcsClient.DownloadAll(ctx, "data/ev_history/"); err != nil {
			log.Fatalf("gcs download ev_history/: %v", err)
		}
	}

	allContents, err := setdata.LoadAllContents(contentsPath)
	if err != nil {
		log.Fatalf("loading contents: %v", err)
	}
	allPullRates, err := setdata.LoadAllPullRates(pullRatesPath)
	if err != nil {
		log.Fatalf("loading pull rates: %v", err)
	}

	// Filter to a single set if -set was provided.
	if *setFlag != "" {
		found := false
		for _, c := range allContents {
			if c.SetID == *setFlag {
				allContents = []setdata.SetContents{c}
				found = true
				break
			}
		}
		if !found {
			log.Fatalf("set %q not found in %s", *setFlag, contentsPath)
		}
	}

	for _, contents := range allContents {
		pullRates, ok := allPullRates[contents.SetID]
		if !ok {
			log.Printf("WARN: no pull rates for set %q — skipping", contents.SetID)
			continue
		}
		fmt.Printf("\n══ %s (%d cards) ══\n", contents.SetID, len(contents.Cards))
		if err := processSet(contents, pullRates); err != nil {
			log.Printf("ERROR processing %s: %v", contents.SetID, err)
		}
	}

	if gcsClient != nil {
		if err := gcsClient.UploadAll(ctx, "data/ev_history/"); err != nil {
			log.Fatalf("gcs upload ev_history/: %v", err)
		}
	}
}

func processSet(contents setdata.SetContents, pullRates *setdata.PullRates) error {
	historyPath := fmt.Sprintf("data/ev_history/%s.json", contents.SetID)

	// Load existing history so partial re-runs don't lose prior data.
	history, err := ev.LoadHistory(historyPath)
	if err != nil {
		return fmt.Errorf("loading ev history: %w", err)
	}
	history.SetID = contents.SetID

	// ── Phase 1: scrape price history for every card ─────────────────────────
	//
	// byMonth: "2023-03" → card number → *CardPrice
	// currentGuides: card number → Full Price Guide map (current prices only)
	//
	// Using pointer values in byMonth so Phase 2 can mutate entries in-place.

	byMonth := make(map[string]map[string]*ev.CardPrice)
	currentGuides := make(map[string]map[string]float64) // card number → guide

	for i, card := range contents.Cards {
		fmt.Printf("[%d/%d] %s  %s  (%s)\n",
			i+1, len(contents.Cards), card.Number, card.Name, card.Rarity)

		if gradedRarities[card.Rarity] {
			guide := scrapeGradedCard(card, byMonth)
			if guide != nil {
				currentGuides[card.Number] = guide
			}
		} else {
			scrapeRegularCard(card, byMonth)
		}

		time.Sleep(politeDelay)
	}

	// ── Phase 2: apply Full Price Guide prices to the most recent month ───────
	//
	// The current guide is already in memory from Phase 1 — no extra HTTP calls.

	latestMonth := latestMonthKey(byMonth)
	if latestMonth != "" {
		for cardNum, guide := range currentGuides {
			if cp, ok := byMonth[latestMonth][cardNum]; ok {
				if cp.GradedPrices == nil {
					cp.GradedPrices = &ev.GradedPrices{}
				}
				applyCurrentGuide(cp.GradedPrices, guide)
			}
		}
		fmt.Printf("\napplied Full Price Guide prices to %d IR/SIR/HR cards for %s\n",
			len(currentGuides), latestMonth)
	}

	// ── Phase 3: calculate metrics and upsert into history ───────────────────
	//
	// Months already recorded in history are skipped — their raw card prices
	// and computed metrics are treated as authoritative and left unchanged.
	// Only months not yet in history are calculated and inserted.

	var inserted, skipped int
	for month, cardMap := range byMonth {
		if history.HasMonth(month) {
			skipped++
			continue
		}
		prices := cardMapToSlice(cardMap)
		m := ev.Calculate(pullRates, prices)
		m.Month = month
		history.Upsert(m)
		inserted++
	}
	fmt.Printf("months: %d inserted, %d skipped (already in history)\n", inserted, skipped)

	// Print summary.
	fmt.Println()
	fmt.Printf("%-10s  %8s  %10s  %10s  %5s  %s\n",
		"month", "ev", "set_value", "top5_value", "cards", "top5_ratio")
	for _, m := range history.Months {
		fmt.Printf("%-10s  %8.2f  %10.2f  %10.2f  %5d  %.3f\n",
			m.Month, m.EV, m.SetValue, m.Top5Value, len(m.CardPrices), m.Top5Ratio)
	}

	if err := ev.SaveHistory(historyPath, history); err != nil {
		return fmt.Errorf("saving history: %w", err)
	}
	fmt.Printf("\nwrote %d months → %s\n", len(history.Months), historyPath)
	return nil
}

// scrapeRegularCard fetches ungraded monthly history for a non-graded card
// (double_rare, ultra_rare) and accumulates it into byMonth.
func scrapeRegularCard(card setdata.Card, byMonth map[string]map[string]*ev.CardPrice) {
	prices, err := pricecharting.Scrape(card.PricechartingURL)
	if err != nil {
		log.Printf("  WARN scrape failed: %v", err)
		return
	}
	fmt.Printf("  %d monthly price(s)\n", len(prices))

	for _, mp := range prices {
		key := mp.SnapshotDate.Format("2006-01")
		ensureMonth(byMonth, key)
		byMonth[key][card.Number] = &ev.CardPrice{
			Number:   card.Number,
			Name:     card.Name,
			Rarity:   card.Rarity,
			PriceUSD: mp.PriceUSD,
		}
	}
}

// scrapeGradedCard fetches ungraded + PSA10 + Grade9 monthly histories and the
// Full Price Guide current prices for an IR/SIR/HR card — all in one HTTP
// request. It accumulates history into byMonth and returns the current guide
// map for the caller to apply to the most recent month in Phase 2.
func scrapeGradedCard(card setdata.Card, byMonth map[string]map[string]*ev.CardPrice) map[string]float64 {
	h, err := pricecharting.ScrapeCardGradedHistory(card.PricechartingURL)
	if err != nil {
		log.Printf("  WARN scrape failed: %v", err)
		return nil
	}
	fmt.Printf("  %d ungraded  %d PSA10  %d grade9  %d guide entries\n",
		len(h.Ungraded), len(h.PSA10), len(h.Grade9), len(h.CurrentGuide))

	// Seed byMonth entries from ungraded history (the authoritative price series).
	for _, mp := range h.Ungraded {
		key := mp.SnapshotDate.Format("2006-01")
		ensureMonth(byMonth, key)
		byMonth[key][card.Number] = &ev.CardPrice{
			Number:       card.Number,
			Name:         card.Name,
			Rarity:       card.Rarity,
			PriceUSD:     mp.PriceUSD,
			GradedPrices: &ev.GradedPrices{},
		}
	}

	// Merge PSA10 monthly prices into existing entries.
	for _, mp := range h.PSA10 {
		key := mp.SnapshotDate.Format("2006-01")
		if cp, ok := byMonth[key][card.Number]; ok {
			v := mp.PriceUSD
			cp.GradedPrices.PSA10 = &v
		}
	}

	// Merge Grade9 monthly prices into existing entries.
	for _, mp := range h.Grade9 {
		key := mp.SnapshotDate.Format("2006-01")
		if cp, ok := byMonth[key][card.Number]; ok {
			v := mp.PriceUSD
			cp.GradedPrices.Grade9 = &v
		}
	}

	return h.CurrentGuide
}

// applyCurrentGuide writes Full Price Guide prices into gp. Only keys present
// in the guide are set; absent graders leave the field nil (omitted in JSON).
func applyCurrentGuide(gp *ev.GradedPrices, guide map[string]float64) {
	if v, ok := guide["psa_10"]; ok {
		gp.PSA10 = &v
	}
	if v, ok := guide["grade_9"]; ok {
		gp.Grade9 = &v
	}
	if v, ok := guide["tag_10"]; ok {
		gp.TAG10 = &v
	}
	if v, ok := guide["ace_10"]; ok {
		gp.ACE10 = &v
	}
	if v, ok := guide["sgc_10"]; ok {
		gp.SGC10 = &v
	}
	if v, ok := guide["cgc_10"]; ok {
		gp.CGC10 = &v
	}
	if v, ok := guide["bgs_10"]; ok {
		gp.BGS10 = &v
	}
	if v, ok := guide["bgs_10_black_label"]; ok {
		gp.BGS10BlackLabel = &v
	}
	if v, ok := guide["cgc_10_pristine"]; ok {
		gp.CGC10Pristine = &v
	}
}

// latestMonthKey returns the lexicographically greatest month key ("YYYY-MM")
// present in byMonth, or "" if the map is empty.
func latestMonthKey(byMonth map[string]map[string]*ev.CardPrice) string {
	keys := make([]string, 0, len(byMonth))
	for k := range byMonth {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return keys[len(keys)-1]
}

// ensureMonth initialises byMonth[key] if it doesn't exist yet.
func ensureMonth(byMonth map[string]map[string]*ev.CardPrice, key string) {
	if byMonth[key] == nil {
		byMonth[key] = make(map[string]*ev.CardPrice)
	}
}

// cardMapToSlice converts the card-number keyed map to a plain slice for
// ev.Calculate.
func cardMapToSlice(m map[string]*ev.CardPrice) []ev.CardPrice {
	out := make([]ev.CardPrice, 0, len(m))
	for _, cp := range m {
		out = append(out, *cp)
	}
	return out
}
