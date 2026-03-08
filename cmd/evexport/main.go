// evexport reads all ev_history/*.json files and produces two CSVs suitable
// for loading into BigQuery:
//
//	ev_metrics.csv     — one row per (set, month): ev, set_value, top_5_value, top_5_ratio
//	ev_card_prices.csv — one row per (set, month, card): raw and graded prices
//
// Empty price cells are left blank so BigQuery interprets them as NULL when
// the destination column is FLOAT64 / NUMERIC.
//
// Usage (from repo root):
//
//	go run ./cmd/evexport                      # all sets
//	go run ./cmd/evexport -set sv01            # single set
//	go run ./cmd/evexport -out data/exports    # custom output directory
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/FutureGadgetResearch/set-value-tracking-backend/internal/ev"
)

const evHistoryGlob = "data/ev_history/*.json"

func main() {
	setFlag := flag.String("set", "", "set ID to export (e.g. sv02); omit to export all sets")
	outDir := flag.String("out", ".", "directory to write the CSV files into")
	flag.Parse()

	paths, err := filepath.Glob(evHistoryGlob)
	if err != nil || len(paths) == 0 {
		log.Fatalf("no ev history files found matching %s", evHistoryGlob)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("creating output directory: %v", err)
	}

	metricsPath := filepath.Join(*outDir, "ev_metrics.csv")
	cardPricesPath := filepath.Join(*outDir, "ev_card_prices.csv")

	metricsFile, err := os.Create(metricsPath)
	if err != nil {
		log.Fatalf("creating %s: %v", metricsPath, err)
	}
	defer metricsFile.Close()

	cardPricesFile, err := os.Create(cardPricesPath)
	if err != nil {
		log.Fatalf("creating %s: %v", cardPricesPath, err)
	}
	defer cardPricesFile.Close()

	mw := csv.NewWriter(metricsFile)
	cw := csv.NewWriter(cardPricesFile)

	if err := mw.Write([]string{
		"set_id", "month", "ev", "set_value", "top_5_value", "top_5_ratio",
	}); err != nil {
		log.Fatalf("writing metrics header: %v", err)
	}
	if err := cw.Write([]string{
		"set_id", "month", "card_number", "card_name", "rarity",
		"raw_price",
		"psa_10_price", "psa_9_price",
		"tag_10_price", "ace_10_price",
		"cgc_10_price",
		"bgs_10_price", "bgs_10_black_label_price",
		"cgc_10_pristine_price",
	}); err != nil {
		log.Fatalf("writing card prices header: %v", err)
	}

	var totalMetrics, totalCards int

	for _, path := range paths {
		history, err := ev.LoadHistory(path)
		if err != nil {
			log.Printf("WARN: loading %s: %v — skipping", path, err)
			continue
		}
		if history.SetID == "" {
			// Derive set ID from filename if the JSON field is missing.
			base := filepath.Base(path)
			history.SetID = base[:len(base)-len(filepath.Ext(base))]
		}

		if *setFlag != "" && history.SetID != *setFlag {
			continue
		}

		fmt.Printf("exporting %s (%d months)\n", history.SetID, len(history.Months))

		for _, m := range history.Months {
			// ev_metrics row
			if err := mw.Write([]string{
				history.SetID,
				m.Month,
				f2(m.EV),
				f2(m.SetValue),
				f2(m.Top5Value),
				f4(m.Top5Ratio),
			}); err != nil {
				log.Fatalf("writing metrics row: %v", err)
			}
			totalMetrics++

			// ev_card_prices rows
			for _, cp := range m.CardPrices {
				row := []string{
					history.SetID,
					m.Month,
					cp.Number,
					cp.Name,
					cp.Rarity,
					f2(cp.PriceUSD),
					fptr(optField(cp.GradedPrices, func(g *ev.GradedPrices) *float64 { return g.PSA10 })),
					fptr(optField(cp.GradedPrices, func(g *ev.GradedPrices) *float64 { return g.Grade9 })),
					fptr(optField(cp.GradedPrices, func(g *ev.GradedPrices) *float64 { return g.TAG10 })),
					fptr(optField(cp.GradedPrices, func(g *ev.GradedPrices) *float64 { return g.ACE10 })),
					fptr(optField(cp.GradedPrices, func(g *ev.GradedPrices) *float64 { return g.CGC10 })),
					fptr(optField(cp.GradedPrices, func(g *ev.GradedPrices) *float64 { return g.BGS10 })),
					fptr(optField(cp.GradedPrices, func(g *ev.GradedPrices) *float64 { return g.BGS10BlackLabel })),
					fptr(optField(cp.GradedPrices, func(g *ev.GradedPrices) *float64 { return g.CGC10Pristine })),
				}
				if err := cw.Write(row); err != nil {
					log.Fatalf("writing card prices row: %v", err)
				}
				totalCards++
			}
		}
	}

	mw.Flush()
	cw.Flush()
	if err := mw.Error(); err != nil {
		log.Fatalf("flushing metrics csv: %v", err)
	}
	if err := cw.Error(); err != nil {
		log.Fatalf("flushing card prices csv: %v", err)
	}

	fmt.Printf("\nwrote %d rows → %s\n", totalMetrics, metricsPath)
	fmt.Printf("wrote %d rows → %s\n", totalCards, cardPricesPath)
}

// optField safely dereferences a *GradedPrices field, returning nil if gp is nil.
func optField(gp *ev.GradedPrices, fn func(*ev.GradedPrices) *float64) *float64 {
	if gp == nil {
		return nil
	}
	return fn(gp)
}

// fptr formats a *float64 as a 2-decimal string, or "" if nil.
// An empty cell is loaded as NULL by BigQuery for FLOAT64 columns.
func fptr(v *float64) string {
	if v == nil {
		return ""
	}
	return f2(*v)
}

// f2 formats a float64 to 2 decimal places.
func f2(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// f4 formats a float64 to 4 decimal places (used for ratios).
func f4(v float64) string {
	return strconv.FormatFloat(v, 'f', 4, 64)
}
