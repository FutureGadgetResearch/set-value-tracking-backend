// evupdate scrapes monthly price history for every card in a set, computes
// EV / set-value / top-5 metrics, and writes new months to BigQuery.
//
// It replaces the separate evbackfill, evcurrent, and evexport commands.
//
// Behaviour:
//   - On first run (empty tables) it backfills every historical month.
//   - On subsequent runs it inserts only months not already present in BQ,
//     so running it monthly as a Cloud Run job is safe and idempotent.
//   - The current month is skipped if it already exists in BQ.
//
// Environment variables:
//
//	GAME          Game to process                (default: pokemon)
//	              Automatically resolves data paths:
//	                pokemon   → data/pokemon/set_contents.json, data/pokemon/set_pull_rates.json
//	                riftbound → data/riftbound/set_contents.json, data/riftbound/set_pull_rates.json
//	CONTENTS_PATH Override set_contents.json path (optional)
//	PULL_RATES_PATH Override set_pull_rates.json path (optional)
//	BQ_PROJECT    BigQuery project ID            (default: future-gadget-labs-483502)
//	BQ_DATASET    BigQuery dataset               (default: tcg_stage)
//	BQ_TABLE_SET  set_market_history table       (default: set_market_history)
//	BQ_TABLE_CARD card_market_history table      (default: card_market_history)
//	GCS_BUCKET    GCS bucket for input files     (optional)
//
// Usage:
//
//	go run ./cmd/evupdate
//	go run ./cmd/evupdate -set sv02
//	GAME=riftbound go run ./cmd/evupdate -set rb01
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	internalbq "github.com/FutureGadgetResearch/set-value-tracking-backend/internal/bq"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/ev"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/gcs"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/pricecharting"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/products"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/setdata"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/tcgplayer"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/civil"
)

const politeDelay = 500 * time.Millisecond

var gradedRarities = map[string]bool{
	"illustration_rare":         true,
	"special_illustration_rare": true,
	"hyper_rare":                true,
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	setFlag := flag.String("set", "", "set ID to process (e.g. sv02); omit to process all sets")
	flag.Parse()

	game := envOr("GAME", "pokemon")
	contentsPath := envOr("CONTENTS_PATH", "data/"+game+"/set_contents.json")
	pullRatesPath := envOr("PULL_RATES_PATH", "data/"+game+"/set_pull_rates.json")

	var defaultProductsPath string
	if game == "pokemon" {
		defaultProductsPath = "data/pokemon/products_all.json"
	} else {
		defaultProductsPath = "data/" + game + "/products.json"
	}
	productsPath := envOr("PRODUCTS_PATH", defaultProductsPath)

	ctx := context.Background()

	// ── GCS: download input files ─────────────────────────────────────────────
	var gcsClient *gcs.Client
	if bucket := os.Getenv("GCS_BUCKET"); bucket != "" {
		var err error
		gcsClient, err = gcs.NewClient(ctx, bucket)
		if err != nil {
			log.Fatalf("creating gcs client: %v", err)
		}
		for _, path := range []string{contentsPath, pullRatesPath, productsPath} {
			if err := gcsClient.Download(ctx, path, path); err != nil {
				log.Fatalf("gcs download %s: %v", path, err)
			}
		}
	}
	_ = gcsClient // no upload needed; BQ is the output

	// ── BigQuery client ───────────────────────────────────────────────────────
	bqProject := envOr("BQ_PROJECT", "future-gadget-labs-483502")
	bqDataset := envOr("BQ_DATASET", "tcg_stage")
	tableSet := envOr("BQ_TABLE_SET", "set_market_history")
	tableCard := envOr("BQ_TABLE_CARD", "card_market_history")
	tableMarket := envOr("BQ_TABLE_MARKET", "tcgplayer_market_snapshots")

	bqClient, err := internalbq.NewClient(ctx, bqProject, bqDataset)
	if err != nil {
		log.Fatalf("creating bq client: %v", err)
	}
	defer bqClient.Close()

	// Query existing (set_id, month) pairs so we can skip already-inserted months.
	existingSet, err := bqClient.ExistingSetMonths(ctx, tableSet)
	if err != nil {
		log.Fatalf("querying existing set months: %v", err)
	}
	fmt.Printf("found %d existing (set_id, month) pairs in %s\n", len(existingSet), tableSet)

	// Load products for market snapshot collection.
	allProds, err := products.Load(productsPath)
	if err != nil {
		log.Printf("WARN: could not load products from %s: %v — market snapshots will be skipped", productsPath, err)
		allProds = nil
	}
	productsBySet := make(map[string][]products.Product)
	for _, p := range allProds {
		productsBySet[p.SetID] = append(productsBySet[p.SetID], p)
	}

	existingMarket, err := bqClient.ExistingMarketSnapshotKeys(ctx, tableMarket)
	if err != nil {
		log.Printf("WARN: could not query existing market snapshots: %v — assuming none exist", err)
		existingMarket = map[string]bool{}
	}
	fmt.Printf("found %d existing market snapshot keys for this month in %s\n", len(existingMarket), tableMarket)

	// ── Load set metadata ─────────────────────────────────────────────────────
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

	// ── Process each set ──────────────────────────────────────────────────────
	var totalInserted int
	for _, contents := range allContents {
		pullRates, ok := allPullRates[contents.SetID]
		if !ok {
			log.Printf("WARN: no pull rates for set %q — skipping", contents.SetID)
			continue
		}
		fmt.Printf("\n══ %s (%d cards) ══\n", contents.SetID, len(contents.Cards))
		existingCard, err := bqClient.ExistingCardMonthPairs(ctx, tableCard, game, contents.SetID)
		if err != nil {
			log.Fatalf("querying existing card month pairs for %s: %v", contents.SetID, err)
		}
		fmt.Printf("found %d existing (card_id, month) pairs in %s for %s\n", len(existingCard), tableCard, contents.SetID)
		n, err := processSet(ctx, bqClient, tableSet, tableCard, existingSet, existingCard, contents, pullRates, game)
		if err != nil {
			log.Printf("ERROR processing %s: %v", contents.SetID, err)
		}
		totalInserted += n
	}

	// ── Collect TCGPlayer market snapshots for this month ─────────────────────
	if len(allProds) > 0 {
		if err := collectMarketSnapshots(ctx, bqClient, tableMarket, existingMarket, allContents, productsBySet, game); err != nil {
			log.Printf("ERROR collecting market snapshots: %v", err)
		}
	}

	if totalInserted == 0 {
		fmt.Println("all entries already up to date — nothing new to insert")
	}
}

func processSet(
	ctx context.Context,
	bqClient *internalbq.Client,
	tableSet, tableCard string,
	existingSet, existingCard map[string]bool,
	contents setdata.SetContents,
	pullRates *setdata.PullRates,
	game string,
) (int, error) {
	// ── Phase 1: scrape price history for every card ──────────────────────────
	byMonth := make(map[string]map[string]*ev.CardPrice)
	currentGuides := make(map[string]map[string]float64)

	currentMonthKey := time.Now().UTC().Format("2006-01")

	for i, card := range contents.Cards {
		cardID := fmt.Sprintf("%s_%s_%s", game, contents.SetID, card.Number)
		if existingCard[cardID+"|"+currentMonthKey+"-01"] {
			fmt.Printf("[%d/%d] %s  %s  — skipped (current month already in BQ)\n",
				i+1, len(contents.Cards), card.Number, card.Name)
			continue
		}

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

	// ── Phase 1.5: fill current-month prices from BQ for skipped cards ──────────
	// Cards already in card_market_history for the current month were skipped
	// in Phase 1. Fetch their RAW prices so set metrics are computed from the
	// full card set, not just the newly-scraped subset.
	{
		bqKey := contents.SetID + "|" + currentMonthKey + "-01"
		if !existingSet[bqKey] {
			// Check if any current-month cards were skipped but are in BQ.
			needFill := false
			for _, c := range contents.Cards {
				cardID := fmt.Sprintf("%s_%s_%s", game, contents.SetID, c.Number)
				if existingCard[cardID+"|"+currentMonthKey+"-01"] {
					if _, scraped := byMonth[currentMonthKey][c.Number]; !scraped {
						needFill = true
						break
					}
				}
			}
			if needFill {
				bqPrices, err := bqClient.FetchRawCardPricesForMonth(ctx, tableCard, game, contents.SetID, currentMonthKey+"-01")
				if err != nil {
					log.Printf("WARN: could not fetch BQ prices for %s/%s: %v", contents.SetID, currentMonthKey, err)
				} else {
					cardInfo := make(map[string]setdata.Card, len(contents.Cards))
					for _, c := range contents.Cards {
						cardInfo[c.Number] = c
					}
					ensureMonth(byMonth, currentMonthKey)
					for num, price := range bqPrices {
						if _, scraped := byMonth[currentMonthKey][num]; !scraped {
							info := cardInfo[num]
							byMonth[currentMonthKey][num] = &ev.CardPrice{
								Number:   num,
								Name:     info.Name,
								Rarity:   info.Rarity,
								PriceUSD: price,
							}
						}
					}
					fmt.Printf("merged %d BQ card prices into current month %s\n", len(bqPrices), currentMonthKey)
				}
			}
		}
	}

	// ── Phase 2: apply Full Price Guide prices to the most recent month ───────
	latestMonth := latestMonthKey(byMonth)
	if latestMonth == "" {
		fmt.Println("no price data scraped or found in BQ — nothing to do")
		return 0, nil
	}
	for cardNum, guide := range currentGuides {
		if cp, ok := byMonth[latestMonth][cardNum]; ok {
			if cp.GradedPrices == nil {
				cp.GradedPrices = &ev.GradedPrices{}
			}
			applyCurrentGuide(cp.GradedPrices, guide)
		}
	}
	fmt.Printf("applied Full Price Guide prices to %d IR/SIR/HR cards for %s\n",
		len(currentGuides), latestMonth)

	// ── Phase 3: build BQ rows for months not already in BQ ──────────────────
	var setRows []internalbq.SetMarketRow
	var cardRows []internalbq.CardMarketRow

	months := sortedKeys(byMonth)
	var insertedSet, insertedCard, skippedSet, skippedCard int

	for _, month := range months {
		bqKey := contents.SetID + "|" + month + "-01"
		prices := cardMapToSlice(byMonth[month])
		m := ev.Calculate(pullRates, prices)
		date := monthToDate(month)

		// Insert set row if not already present. If a prior run produced a
		// partial entry (e.g. ev=0 due to incomplete scraping), delete it in
		// BQ with:
		//   DELETE FROM `dataset.set_market_history`
		//   WHERE set_id='rb02' AND month='2026-03-01'
		// then re-run so the corrected row is inserted.
		if !existingSet[bqKey] {
			setRows = append(setRows, internalbq.SetMarketRow{
				Game:      game,
				SetID:     contents.SetID,
				Month:     date,
				EV:        m.EV,
				SetValue:  m.SetValue,
				Top5Value: m.Top5Value,
				Top5Ratio: m.Top5Ratio,
			})
			insertedSet++
		} else {
			skippedSet++
		}

		for _, cp := range prices {
			cardID := fmt.Sprintf("%s_%s_%s", game, contents.SetID, cp.Number)
			if existingCard[cardID+"|"+month+"-01"] {
				skippedCard++
				continue
			}
			cardRows = append(cardRows, cardMarketRows(game, contents.SetID, date, cp)...)
			insertedCard++
		}
	}

	fmt.Printf("set rows:  %d to insert, %d skipped\n", insertedSet, skippedSet)
	fmt.Printf("card rows: %d months to insert, %d skipped\n", insertedCard, skippedCard)

	// ── Phase 4: write to BQ ──────────────────────────────────────────────────
	if len(setRows) > 0 {
		if err := bqClient.InsertSetRows(ctx, tableSet, setRows); err != nil {
			return 0, fmt.Errorf("inserting set rows: %w", err)
		}
		fmt.Printf("inserted %d rows → %s\n", len(setRows), tableSet)
	}

	if len(cardRows) > 0 {
		if err := bqClient.InsertCardRows(ctx, tableCard, cardRows); err != nil {
			return 0, fmt.Errorf("inserting card rows: %w", err)
		}
		fmt.Printf("inserted %d rows → %s\n", len(cardRows), tableCard)
	}

	return insertedSet + insertedCard, nil
}

// cardMarketRows expands one CardPrice into one row per grade with a price.
func cardMarketRows(game, setID string, date civil.Date, cp ev.CardPrice) []internalbq.CardMarketRow {
	cardID := fmt.Sprintf("%s_%s_%s", game, setID, cp.Number)

	rows := []internalbq.CardMarketRow{{
		CardID:      cardID,
		Month:       date,
		GradeID:     "RAW",
		MarketPrice: bigquery.NullFloat64{Float64: cp.PriceUSD, Valid: true},
	}}

	gp := cp.GradedPrices
	if gp == nil {
		return rows
	}

	type gradeEntry struct {
		id    string
		price *float64
	}
	grades := []gradeEntry{
		{"PSA_10", gp.PSA10},
		{"GRADE_9", gp.Grade9},
		{"TAG_10", gp.TAG10},
		{"ACE_10", gp.ACE10},
		{"SGC_10", gp.SGC10},
		{"CGC_10", gp.CGC10},
		{"BGS_10", gp.BGS10},
		{"BGS_10_BL", gp.BGS10BlackLabel},
		{"CGC_10_PRISTINE", gp.CGC10Pristine},
	}
	for _, g := range grades {
		if g.price != nil {
			rows = append(rows, internalbq.CardMarketRow{
				CardID:      cardID,
				Month:       date,
				GradeID:     g.id,
				MarketPrice: bigquery.NullFloat64{Float64: *g.price, Valid: true},
			})
		}
	}
	return rows
}

// monthToDate converts a "YYYY-MM" string to a civil.Date for the 1st of that month.
func monthToDate(month string) civil.Date {
	t, _ := time.Parse("2006-01", month)
	return civil.Date{Year: t.Year(), Month: t.Month(), Day: 1}
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
	keys := sortedKeys(byMonth)
	if len(keys) == 0 {
		return ""
	}
	return keys[len(keys)-1]
}

func sortedKeys(byMonth map[string]map[string]*ev.CardPrice) []string {
	keys := make([]string, 0, len(byMonth))
	for k := range byMonth {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

func collectMarketSnapshots(
	ctx context.Context,
	bqClient *internalbq.Client,
	tableMarket string,
	existingMarketKeys map[string]bool,
	allContents []setdata.SetContents,
	productsBySet map[string][]products.Product,
	game string,
) error {
	now := time.Now().UTC()
	today := civil.Date{Year: now.Year(), Month: now.Month(), Day: 1}

	processedSets := make(map[string]bool, len(allContents))
	for _, c := range allContents {
		processedSets[c.SetID] = true
	}

	var rows []internalbq.TCGPlayerMarketRow

	for setID := range processedSets {
		for _, p := range productsBySet[setID] {
			if p.TCGPlayerID == "" {
				continue
			}
			key := game + "|" + setID + "|" + p.ProductType
			if existingMarketKeys[key] {
				fmt.Printf("  %s %s — market snapshot already in BQ today, skipping\n", setID, p.ProductType)
				continue
			}

			fmt.Printf("  scraping TCGPlayer market snapshot: %s %s (id=%s)...\n", setID, p.ProductType, p.TCGPlayerID)

			tcgMetrics, err := tcgplayer.ScrapeCurrentMetrics(p.TCGPlayerID)
			if err != nil {
				log.Printf("  WARN tcgplayer scrape failed for %s %s: %v", setID, p.ProductType, err)
				continue
			}

			var avgSold float64
			if s, scrapeErr := pricecharting.ScrapeSoldLast30Days(p.PricechartingURL); scrapeErr != nil {
				log.Printf("  WARN avg_sold_30d scrape failed for %s %s: %v", setID, p.ProductType, scrapeErr)
			} else {
				avgSold = s
			}

			row := internalbq.TCGPlayerMarketRow{
				SnapshotDate:   today,
				TCG:            game,
				SetID:          setID,
				ProductType:    p.ProductType,
				TCGPlayerID:    p.TCGPlayerID,
				SellerCount:    tcgMetrics.SellerCount,
				ProductCount:   tcgMetrics.ProductCount,
				MedianAskPrice: tcgMetrics.MedianAskPrice,
				AvgSold30d:     avgSold,
			}
			if tcgMetrics.ProductCount > 0 && avgSold > 0 {
				ratio := avgSold / float64(tcgMetrics.ProductCount)
				row.SalesToInventoryRatio = bigquery.NullFloat64{Float64: ratio, Valid: true}
			}

			rows = append(rows, row)
			time.Sleep(politeDelay)
		}
	}

	if len(rows) == 0 {
		fmt.Println("no market snapshot rows to insert")
		return nil
	}

	if err := bqClient.InsertMarketSnapshotRows(ctx, tableMarket, rows); err != nil {
		return fmt.Errorf("inserting market snapshot rows: %w", err)
	}
	fmt.Printf("inserted %d market snapshot row(s) → %s\n", len(rows), tableMarket)
	return nil
}
