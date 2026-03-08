# Project Overview

## Purpose

This backend tracks the market value of Pokémon TCG sealed products and individual cards over time. It writes monthly snapshots to a Google Sheet for analysis. Two distinct pipelines run independently:

1. **Product pipeline** (`main.go`) — tracks booster box/product prices from PriceCharting and listing metrics from TCGPlayer.
2. **EV pipeline** (`cmd/evbackfill/main.go`) — tracks individual card prices, computes expected value (EV), set value, and top-5 card value per month per set.

---

## Repository Layout

```
set-value-tracking-backend/
│
├── main.go                         # Product pipeline entry point
│
├── cmd/
│   ├── evbackfill/main.go          # EV backfill command (run before main)
│   └── tcgdebug/main.go            # Debug tool: dumps TCGPlayer page HTML
│
├── internal/
│   ├── products/products.go        # Loads data/products.json → []Product
│   ├── setdata/setdata.go          # Loads contents.json + pull_rates.json
│   ├── ev/ev.go                    # EV calculation + ev_history.json I/O
│   ├── pricecharting/pricecharting.go  # PriceCharting scraper
│   ├── tcgplayer/tcgplayer.go      # TCGPlayer scraper (uses headless Chrome)
│   └── sheets/sheets.go            # Google Sheets API client
│
├── data/
│   ├── products.json               # List of sealed products to track
│   └── sets/
│       └── sv01/
│           ├── pull_rates.json     # Pack slot pull rates by rarity
│           ├── contents.json       # Card list with rarities + PriceCharting URLs
│           └── ev_history.json     # Generated: monthly EV cache (do not edit by hand)
│
├── credentials.json                # Google service account key (gitignored)
└── doc/                            # This documentation
```

---

## Data Flow

### Product Pipeline (`main.go`)

```
data/products.json
        │
        ▼
products.Load()
        │
        ├──► pricecharting.Scrape(product URL)
        │         └── monthly price history → one row per month per product
        │
        ├──► pricecharting.ScrapeSoldLast30Days(product URL)
        │         └── avg_sold_30d → most recent row only
        │
        ├──► tcgplayer.ScrapeCurrentMetrics(product URL)
        │         └── median_ask, product_count, seller_count → most recent row only
        │
        ├──► ev.LoadHistory("data/sets/{set_id}/ev_history.json")
        │         └── ev, set_value, top_5_value, top_5_ratio → all rows
        │
        └──► sheets.AppendRows("product_tracking")
                  └── deduplicates by (snapshot_date, set_id, product_type)
```

### EV Pipeline (`cmd/evbackfill`)

```
data/sets/sv01/contents.json
data/sets/sv01/pull_rates.json
        │
        ▼
Phase 1 ─ scrape each card (72 HTTP calls + 500ms delay each)
  │
  ├── double_rare / ultra_rare cards:
  │       pricecharting.Scrape(card URL)
  │           └── ungraded monthly prices
  │
  └── illustration_rare / special_illustration_rare / hyper_rare cards:
          pricecharting.ScrapeCardGradedHistory(card URL)  ← single HTTP call
              ├── ungraded monthly prices       (from VGPC.chart_data "used")
              ├── PSA10 monthly prices          (from VGPC.chart_data "manualonly")
              ├── Grade9 monthly prices         (from VGPC.chart_data "graded")
              └── CurrentGuide map              (from Full Price Guide table:
                                                 TAG10, ACE10, SGC10, CGC10,
                                                 BGS10, BGS10BL, CGC10Pristine)

Phase 2 ─ apply Full Price Guide prices to most recent month (no extra HTTP calls)
  │
  └── for each IR/SIR/HR card: write CurrentGuide → GradedPrices fields

Phase 3 ─ compute + persist
  │
  └── ev.Calculate(pull_rates, card_prices)
          ├── ev          = Σ (pull_rate_per_pack × packs_per_box × avg_rarity_price)
          ├── set_value   = Σ card prices
          ├── top_5_value = sum of 5 highest ungraded prices
          └── top_5_ratio = top_5_value / set_value
              └──► ev_history.json (upserted by month)
```

---

## Key Design Decisions

| Decision | Rationale |
|---|---|
| `ev_history.json` caches computed monthly EV | Avoids re-scraping 72 cards × N months every run; historical card prices never change |
| EV uses ungraded prices only | Cards come out of packs ungraded; graded prices are informational |
| Graded prices (PSA10, Grade9) tracked historically | Same chart_data page, no extra HTTP cost |
| Current-month grader prices (SGC, CGC, BGS, etc.) | Only meaningful for the most recent month; too expensive to track historically |
| Dedup by (date, set_id, product_type) before writing | Safe to re-run without duplicating sheet rows |
| 429 retry with Retry-After | PriceCharting rate-limits bulk scrapers; honouring the header avoids bans |
| TCGPlayer uses headless Chrome | TCGPlayer is a React SPA; raw HTTP returns empty page |

---

## Environment & Credentials

| Item | Details |
|---|---|
| `credentials.json` | Google service account JSON key. Must be granted **Editor** access to the target spreadsheet. |
| `SPREADSHEET_ID` env var | The Google Sheets spreadsheet ID (from the sheet URL). Required by `main.go`. |
| Chrome / Chromium | Must be installed and on PATH for `tcgplayer` package (headless Chrome via `chromedp`). |

---

## Running Order

When setting up a new set or updating an existing one:

```
1. go run ./cmd/evbackfill     # populate ev_history.json (one-time + monthly refresh)
2. SPREADSHEET_ID=xxx go run . # scrape products + write to sheet
```

The EV backfill must run before `main.go` for EV columns to be populated. If `ev_history.json` is absent, `main.go` silently leaves those columns empty.
