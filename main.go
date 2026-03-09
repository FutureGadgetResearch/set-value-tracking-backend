package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/gcs"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/pricecharting"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/products"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/setmetrics"
	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/tcgplayer"
)

const defaultOutputCSV = "data/product_pricing.csv"

var header = []string{
	"snapshot_date", "tcg", "set_id", "en_set_id", "era", "release_date",
	"is_special_set", "is_standard_legal",
	"product_type", "msrp", "market_price",
	"ev", "price_change_90d",
	"seller_count", "product_count", "avg_sold_30d",
	"sales_to_inventory_ratio", "median_ask_price",
	"set_value", "top_5_value", "top_5_ratio",
	"price_increase_next_90_days", "price_increase_next_365_days",
	"price_increase_next_2_years", "price_increase_next_5_years",
}

func main() {
	ctx := context.Background()

	outputCSV := os.Getenv("OUTPUT_CSV")
	if outputCSV == "" {
		outputCSV = defaultOutputCSV
	}

	// --- Game selection ---
	game := os.Getenv("GAME")
	if game == "" {
		game = "pokemon"
	}
	var productsPath string
	switch game {
	case "onepiece":
		productsPath = "data/onepiece/products.json"
	case "onepiecejp":
		productsPath = "data/onepiecejp/products.json"
	case "hololive":
		productsPath = "data/hololive/products.json"
	case "hololivejp":
		productsPath = "data/hololivejp/products.json"
	case "pokemonjp":
		productsPath = "data/pokemonjp/products.json"
	case "weissschwarz-en":
		productsPath = "data/weissschwarz/products_en.json"
	case "weissschwarz-jp":
		productsPath = "data/weissschwarz/products_jp.json"
	case "magic":
		productsPath = "data/magic/products.json"
	case "riftbound":
		productsPath = "data/riftbound/products.json"
	default:
		productsPath = "data/pokemon/products_all.json"
	}
	fmt.Printf("game=%s  products=%s\n", game, productsPath)

	// --- GCS: download input files when running in cloud mode ---
	var gcsClient *gcs.Client
	if bucket := os.Getenv("GCS_BUCKET"); bucket != "" {
		var err error
		gcsClient, err = gcs.NewClient(ctx, bucket)
		if err != nil {
			log.Fatalf("creating gcs client: %v", err)
		}
		if err := gcsClient.Download(ctx, productsPath, productsPath); err != nil {
			log.Fatalf("gcs download %s: %v", productsPath, err)
		}
		if err := gcsClient.Download(ctx, "data/set_metrics.json", "data/set_metrics.json"); err != nil {
			log.Fatalf("gcs download set_metrics.json: %v", err)
		}
	}

	// --- Products ---
	prods, err := products.Load(productsPath)
	if err != nil {
		log.Fatalf("loading products: %v", err)
	}
	fmt.Printf("loaded %d product(s)\n", len(prods))

	// Optional filters (comma-separated env vars).
	if setIDs := os.Getenv("SET_IDS"); setIDs != "" {
		allowed := make(map[string]struct{})
		for _, id := range strings.Split(setIDs, ",") {
			allowed[strings.TrimSpace(id)] = struct{}{}
		}
		var filtered []products.Product
		for _, p := range prods {
			if _, ok := allowed[p.SetID]; ok {
				filtered = append(filtered, p)
			}
		}
		fmt.Printf("filtered to %d product(s) for SET_IDS=%s\n", len(filtered), setIDs)
		prods = filtered
	}
	if productTypes := os.Getenv("PRODUCT_TYPES"); productTypes != "" {
		allowed := make(map[string]struct{})
		for _, pt := range strings.Split(productTypes, ",") {
			allowed[strings.TrimSpace(pt)] = struct{}{}
		}
		var filtered []products.Product
		for _, p := range prods {
			if _, ok := allowed[p.ProductType]; ok {
				filtered = append(filtered, p)
			}
		}
		fmt.Printf("filtered to %d product(s) for PRODUCT_TYPES=%s\n", len(filtered), productTypes)
		prods = filtered
	}

	// --- Set metrics (ev, set_value, top_5_value, top_5_ratio) ---
	//
	// Source priority:
	//   1. SET_METRICS_URL env var  → fetch from HTTP
	//   2. GCS_BUCKET set           → already downloaded to data/set_metrics.json above
	//   3. default                  → read data/set_metrics.json from local disk
	var sm *setmetrics.Metrics
	if url := os.Getenv("SET_METRICS_URL"); url != "" {
		sm, err = setmetrics.LoadFromURL(url)
		if err != nil {
			log.Fatalf("loading set metrics from URL: %v", err)
		}
		fmt.Printf("loaded set metrics from URL: %s\n", url)
	} else {
		sm, err = setmetrics.LoadFromFile("data/set_metrics.json")
		if err != nil {
			log.Fatalf("loading set metrics from file: %v", err)
		}
		fmt.Println("loaded set metrics from data/set_metrics.json")
	}

	// --- Scrape and build rows ---
	var allRows [][]string

	for _, p := range prods {
		fmt.Printf("scraping %s %s (%s)...\n", p.TCG, p.SetID, p.ProductType)

		prices, err := pricecharting.Scrape(p.PricechartingURL)
		if err != nil {
			log.Printf("  pricecharting scrape failed: %v", err)
			continue
		}
		fmt.Printf("  got %d monthly price(s) from pricecharting\n", len(prices))

		priceByMonth := make(map[time.Time]float64, len(prices))
		for _, mp := range prices {
			priceByMonth[mp.SnapshotDate] = mp.PriceUSD
		}

		rows := make([][]string, len(prices))
		for i, mp := range prices {
			monthKey := mp.SnapshotDate.Format("2006-01")
			m, hasMetrics := sm.Lookup(p.SetID, monthKey)
			rows[i] = buildRow(p, mp, priceByMonth, m, hasMetrics)
		}

		// avg_sold_30d and TCGPlayer metrics — most recent row only.
		// Both are scraped before writing so sales_to_inventory_ratio can be derived.
		last := rows[len(rows)-1]

		var soldLast30 float64
		if s, scrapeErr := pricecharting.ScrapeSoldLast30Days(p.PricechartingURL); scrapeErr != nil {
			log.Printf("  sold listings scrape failed: %v", scrapeErr)
		} else {
			soldLast30 = s
			fmt.Printf("  avg_sold_30d = %.0f\n", soldLast30)
			last[colIndex("avg_sold_30d")] = fmt.Sprintf("%.0f", soldLast30)
		}

		if tcgMetrics, scrapeErr := tcgplayer.ScrapeCurrentMetrics(p.TCGPlayerID); scrapeErr != nil {
			log.Printf("  tcgplayer scrape failed: %v", scrapeErr)
		} else {
			fmt.Printf("  tcgplayer: median_ask=%.2f  product_count=%d  seller_count=%d\n",
				tcgMetrics.MedianAskPrice, tcgMetrics.ProductCount, tcgMetrics.SellerCount)
			last[colIndex("median_ask_price")] = fmt.Sprintf("%.2f", tcgMetrics.MedianAskPrice)
			last[colIndex("product_count")] = fmt.Sprint(tcgMetrics.ProductCount)
			last[colIndex("seller_count")] = fmt.Sprint(tcgMetrics.SellerCount)
			if tcgMetrics.ProductCount > 0 && soldLast30 > 0 {
				last[colIndex("sales_to_inventory_ratio")] = fmt.Sprintf("%.4f", soldLast30/float64(tcgMetrics.ProductCount))
			}
		}

		allRows = append(allRows, rows...)
	}

	// --- Deduplication: skip rows already present in the CSV ---
	existing, err := readExistingCSV(outputCSV)
	if err != nil {
		log.Fatalf("reading existing CSV: %v", err)
	}

	type rowKey struct{ date, setID, productType string }
	existingKeys := make(map[rowKey]struct{}, len(existing))
	for _, r := range existing {
		k := rowKey{
			date:        csvGet(r, colIndex("snapshot_date")),
			setID:       csvGet(r, colIndex("set_id")),
			productType: csvGet(r, colIndex("product_type")),
		}
		existingKeys[k] = struct{}{}
	}

	var newRows [][]string
	for _, row := range allRows {
		k := rowKey{
			date:        row[colIndex("snapshot_date")],
			setID:       row[colIndex("set_id")],
			productType: row[colIndex("product_type")],
		}
		if _, exists := existingKeys[k]; !exists {
			newRows = append(newRows, row)
		}
	}

	if len(newRows) == 0 {
		fmt.Println("no new rows to write — CSV is already up to date.")
		return
	}

	fmt.Printf("appending %d new row(s) to %s...\n", len(newRows), outputCSV)
	if err := appendToCSV(outputCSV, newRows, len(existing) == 0); err != nil {
		log.Fatalf("writing CSV: %v", err)
	}
	fmt.Println("done.")
}

// colIndex returns the 0-based index for a named column.
func colIndex(name string) int {
	for i, h := range header {
		if h == name {
			return i
		}
	}
	panic("unknown column: " + name)
}

// buildRow maps a Product + MonthlyPrice into the full header column order.
// Current-only columns are left empty for historical rows.
func buildRow(p products.Product, mp pricecharting.MonthlyPrice, priceByMonth map[time.Time]float64, m setmetrics.Entry, hasMetrics bool) []string {
	row := make([]string, len(header))
	row[colIndex("snapshot_date")] = mp.SnapshotDate.Format("2006-01-02")
	row[colIndex("tcg")] = p.TCG
	row[colIndex("set_id")] = p.SetID
	row[colIndex("en_set_id")] = p.EnSetID
	row[colIndex("era")] = p.Era
	row[colIndex("release_date")] = p.ReleaseDate
	row[colIndex("is_special_set")] = fmt.Sprint(p.IsSpecialSet)
	row[colIndex("is_standard_legal")] = fmt.Sprint(standardLegal(p, mp.SnapshotDate))
	row[colIndex("product_type")] = p.ProductType
	row[colIndex("msrp")] = fmt.Sprintf("%.2f", p.MSRP)
	row[colIndex("market_price")] = fmt.Sprintf("%.2f", mp.PriceUSD)
	row[colIndex("price_change_90d")] = priceChange90d(mp, priceByMonth)
	if hasMetrics {
		row[colIndex("ev")] = fmt.Sprintf("%.2f", m.EV)
		row[colIndex("set_value")] = fmt.Sprintf("%.2f", m.SetValue)
		row[colIndex("top_5_value")] = fmt.Sprintf("%.2f", m.Top5Value)
		row[colIndex("top_5_ratio")] = fmt.Sprintf("%.4f", m.Top5Ratio)
	}
	// Prediction columns: back-filled from actual future prices where the data exists.
	// Left empty for rows that are too recent for the horizon to have passed yet.
	row[colIndex("price_increase_next_90_days")] = priceChangeForward(mp, priceByMonth, 3)
	row[colIndex("price_increase_next_365_days")] = priceChangeForward(mp, priceByMonth, 12)
	row[colIndex("price_increase_next_2_years")] = priceChangeForward(mp, priceByMonth, 24)
	row[colIndex("price_increase_next_5_years")] = priceChangeForward(mp, priceByMonth, 60)
	// seller_count, product_count, avg_sold_30d, sales_to_inventory_ratio, median_ask_price:
	// filled in by main() for the latest row only.
	return row
}

func standardLegal(p products.Product, snapshotDate time.Time) bool {
	if p.StandardLegalUntil == "" {
		return true
	}
	until, err := time.Parse("2006-01-02", p.StandardLegalUntil)
	if err != nil {
		return true
	}
	return snapshotDate.Before(until)
}

func priceChange90d(mp pricecharting.MonthlyPrice, priceByMonth map[time.Time]float64) string {
	prev := mp.SnapshotDate.AddDate(0, -3, 0)
	prevPrice, ok := priceByMonth[prev]
	if !ok || prevPrice == 0 {
		return ""
	}
	return fmt.Sprintf("%.6f", (mp.PriceUSD-prevPrice)/prevPrice)
}

// priceChangeForward returns the percentage price change from mp.SnapshotDate to
// +months months later. Returns "" if the future month is not yet in priceByMonth.
func priceChangeForward(mp pricecharting.MonthlyPrice, priceByMonth map[time.Time]float64, months int) string {
	if mp.PriceUSD == 0 {
		return ""
	}
	future := mp.SnapshotDate.AddDate(0, months, 0)
	futurePrice, ok := priceByMonth[future]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%.6f", (futurePrice-mp.PriceUSD)/mp.PriceUSD)
}

// readExistingCSV opens the CSV at path and returns all data rows (skipping the
// header). Returns an empty slice if the file does not exist yet.
func readExistingCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[1:], nil // skip header row
}

// appendToCSV appends rows to the CSV at path. If writeHeader is true, the
// header row is written first (used when creating the file from scratch).
func appendToCSV(path string, rows [][]string, writeHeader bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if writeHeader {
		if err := w.Write(header); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func csvGet(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}
