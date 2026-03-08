# Runbook

Operational reference for running, maintaining, and extending the application.

---

## Prerequisites

| Requirement | Notes |
|---|---|
| Go 1.25+ | `go version` to check |
| Chrome / Chromium | Required by `tcgplayer` package for headless rendering. Must be on PATH. |
| `credentials.json` | Google service account key in the repo root. The account needs **Editor** access to the target spreadsheet. |
| `SPREADSHEET_ID` | Environment variable containing the Google Sheets spreadsheet ID (the long ID in the sheet URL). |

---

## Commands

### EV Backfill — `cmd/evbackfill`

Scrapes PriceCharting for every card in `data/sets/sv01/contents.json` and writes `data/sets/sv01/ev_history.json`.

```bash
# Run from the repo root
go run ./cmd/evbackfill
```

**When to run:**
- Once initially to populate all historical months.
- Monthly to add the current month's data before running the main pipeline.
- After adding new cards to `contents.json`.

**What it does:**
1. Scrapes ungraded monthly price history for all 72 cards (~72 HTTP calls, 500ms apart).
2. For IR/SIR/HR cards: also scrapes PSA 10 + Grade 9 from the same page.
3. For the most recent month's IR/SIR/HR cards: scrapes 7 grader sub-pages each (up to 280 additional calls).
4. Computes EV, set value, top-5 for every month and upserts into `ev_history.json`.

**Rate limiting:** 429 responses are handled automatically — the scraper waits for the duration specified in the `Retry-After` header (default 60 s) and retries up to 5 times.

**Partial failure:** If a card fails to scrape, it logs a warning and continues. The card will be absent from affected months. Re-run after the issue resolves; existing months are upserted, not duplicated.

**Expected output:**
```
[1/72] 019  Spidops ex  (double_rare)
  24 monthly price(s)
[2/72] 032  Arcanine ex  (double_rare)
  24 monthly price(s)
...
[33/72] 199  Tarountula  (illustration_rare)
  24 ungraded  24 PSA10  0 grade9 monthly price(s)
...

scraping current graded prices for 2025-01...
  199 Tarountula tag_10 = $12.50
  ...

month       ev        set_value   top5_value  cards  top5_ratio
2023-03   142.50      850.25      320.10      68     0.376
...

wrote 24 months → data/sets/sv01/ev_history.json
```

---

### Product Pipeline — `main.go`

Scrapes sealed product prices and writes rows to the Google Sheet.

```bash
SPREADSHEET_ID=your-sheet-id go run .
```

**What it does:**
1. Loads `data/products.json` and `ev_history.json` for each set.
2. For each product: scrapes full price history from PriceCharting (one row per month).
3. For the most recent month: scrapes TCGPlayer for listing counts and median ask.
4. Deduplicates against existing sheet rows.
5. Appends only new rows to the `product_tracking` tab.

**Expected output:**
```
loaded 1 product(s)
loaded ev history for sv01 (24 months)
scraping pokemon sv01 (booster-box)...
  got 24 monthly price(s) from pricecharting
  avg_sold_30d = 42
  tcgplayer: median_ask=148.50  product_count=312  seller_count=89
appending 1 new row(s)...
done.
```

---

### TCG Debug Tool — `cmd/tcgdebug`

Dumps the raw rendered HTML of a TCGPlayer product page to stdout. Used for inspecting the DOM structure when TCGPlayer changes their layout.

```bash
go run ./cmd/tcgdebug
```

The URL is hardcoded in `cmd/tcgdebug/main.go`. Edit it before running.

---

## Adding a New Set

1. **Add a product entry** to `data/products.json`:
   ```json
   {
     "set_id": "sv02",
     "tcg": "pokemon",
     "era": "Scarlet & Violet",
     "release_date": "2023-06-09",
     "is_special_set": false,
     "standard_legal_until": "",
     "product_type": "booster-box",
     "msrp": 143.64,
     "pricecharting_url": "...",
     "tcgplayer_url": "..."
   }
   ```

2. **Create the set data directory:**
   ```
   data/sets/sv02/
   ```

3. **Create `pull_rates.json`** — copy `data/sets/sv01/pull_rates.json` and update the set_id and any rarity rates that differ for the new set.

4. **Create `contents.json`** — populate with every non-bulk card (double rare and above):
   ```json
   {
     "set_id": "sv02",
     "cards": [
       {
         "number": "001",
         "name": "...",
         "rarity": "double_rare",
         "pricecharting_url": "..."
       }
     ]
   }
   ```
   PriceCharting URL pattern: `https://www.pricecharting.com/game/pokemon-scarlet-&-violet/{name-slug}-{number}`

5. **Run the EV backfill** — update the constants at the top of `cmd/evbackfill/main.go` to point to the new set paths, then run:
   ```bash
   go run ./cmd/evbackfill
   ```

6. **Run the main pipeline** normally — EV columns will now be populated for the new set.

> **Note:** Currently `cmd/evbackfill/main.go` has the paths to `sv01` hardcoded. When tracking multiple sets, either parameterise the command with a `--set` flag or run separate instances with different constants.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `VGPC.chart_data not found on page` | PriceCharting blocked the request or changed page structure | Wait and retry; check if chart_data JS variable name changed |
| `429 rate-limited` appearing repeatedly | Too many requests in a short window | Normal — the scraper will wait and retry automatically. If persistent, increase `politeDelay` in evbackfill |
| TCGPlayer scrape hangs or times out | Chrome not installed / JS slow to render | Ensure Chromium is installed; try increasing the 90s timeout in `tcgplayer.go` |
| EV columns empty in sheet | `ev_history.json` missing or not covering those months | Run `cmd/evbackfill` first |
| `no Normal printing found in pricepoints` | TCGPlayer API changed or the product has no Normal listings | Check the pricepoints API response manually |
| Duplicate rows in sheet | Dedup key changed (e.g. `product_type` value changed) | Delete duplicates manually in the sheet |
