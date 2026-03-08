// evcurrent scrapes the current prices for every card in a set's contents.json,
// computes EV / set-value / top-5-value / top-5-ratio for the current month,
// and upserts the result into ev_history.json — overwriting any existing entry
// for this month so the data stays fresh.
//
// The logic mirrors evbackfill (same scraping phases) but only processes the
// most recent month returned by PriceCharting instead of every historical month.
//
// Usage (from repo root):
//
//	go run ./cmd/evcurrent             # all sets
//	go run ./cmd/evcurrent -set sv01   # single set
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

	politeDelay = 500 * time.Millisecond
)

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

	history, err := ev.LoadHistory(historyPath)
	if err != nil {
		return fmt.Errorf("loading ev history: %w", err)
	}
	history.SetID = contents.SetID

	// ── Phase 1: scrape current prices for every card ────────────────────────

	byMonth := make(map[string]map[string]*ev.CardPrice)
	currentGuides := make(map[string]map[string]float64)

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

	latestMonth := latestMonthKey(byMonth)
	if latestMonth == "" {
		fmt.Println("no price data scraped — nothing to do")
		return nil
	}

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

	// ── Phase 3: calculate metrics and upsert current month ──────────────────
	//
	// Unlike evbackfill, we always overwrite the current month entry so that
	// the history reflects today's prices.

	prices := cardMapToSlice(byMonth[latestMonth])
	m := ev.Calculate(pullRates, prices)
	m.Month = latestMonth
	history.Upsert(m)

	fmt.Printf("\nupdated %s → ev=%.2f  set_value=%.2f  top5_value=%.2f  top5_ratio=%.3f\n",
		latestMonth, m.EV, m.SetValue, m.Top5Value, m.Top5Ratio)

	if err := ev.SaveHistory(historyPath, history); err != nil {
		return fmt.Errorf("saving history: %w", err)
	}
	fmt.Printf("wrote %d months → %s\n", len(history.Months), historyPath)
	return nil
}

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

func scrapeGradedCard(card setdata.Card, byMonth map[string]map[string]*ev.CardPrice) map[string]float64 {
	h, err := pricecharting.ScrapeCardGradedHistory(card.PricechartingURL)
	if err != nil {
		log.Printf("  WARN scrape failed: %v", err)
		return nil
	}
	fmt.Printf("  %d ungraded  %d PSA10  %d grade9  %d guide entries\n",
		len(h.Ungraded), len(h.PSA10), len(h.Grade9), len(h.CurrentGuide))

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

	for _, mp := range h.PSA10 {
		key := mp.SnapshotDate.Format("2006-01")
		if cp, ok := byMonth[key][card.Number]; ok {
			v := mp.PriceUSD
			cp.GradedPrices.PSA10 = &v
		}
	}

	for _, mp := range h.Grade9 {
		key := mp.SnapshotDate.Format("2006-01")
		if cp, ok := byMonth[key][card.Number]; ok {
			v := mp.PriceUSD
			cp.GradedPrices.Grade9 = &v
		}
	}

	return h.CurrentGuide
}

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

func ensureMonth(byMonth map[string]map[string]*ev.CardPrice, key string) {
	if byMonth[key] == nil {
		byMonth[key] = make(map[string]*ev.CardPrice)
	}
}

func cardMapToSlice(m map[string]*ev.CardPrice) []ev.CardPrice {
	out := make([]ev.CardPrice, 0, len(m))
	for _, cp := range m {
		out = append(out, *cp)
	}
	return out
}
