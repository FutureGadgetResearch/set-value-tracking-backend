# Package Reference

This file documents every internal package: its responsibility, exported types, and exported functions. Use this as the authoritative contract when modifying or extending the code.

---

## `internal/products`

**File:** `internal/products/products.go`
**Responsibility:** Deserialise `data/products.json` into Go structs.

### Types

```go
type Product struct {
    SetID              string  // e.g. "sv01"
    TCG                string  // e.g. "pokemon"
    Era                string  // e.g. "Scarlet & Violet"
    ReleaseDate        string  // "YYYY-MM-DD"
    IsSpecialSet       bool
    StandardLegalUntil string  // "YYYY-MM-DD"; empty = still legal
    ProductType        string  // e.g. "booster-box"
    MSRP               float64
    PricechartingURL   string
    TCGPlayerURL       string
}
```

### Functions

```go
// Load reads products.json at path and returns the product list.
func Load(path string) ([]Product, error)
```

---

## `internal/setdata`

**File:** `internal/setdata/setdata.go`
**Responsibility:** Deserialise per-set `contents.json` and `pull_rates.json`.

### Types

```go
type Card struct {
    Number           string  // zero-padded card number, e.g. "045"
    Name             string  // e.g. "Gyarados ex"
    Rarity           string  // see rarity constants below
    PricechartingURL string
}

type SetContents struct {
    SetID string
    Cards []Card
}

type RarityRate struct {
    Rarity            string
    Slot              string  // which physical pack slot this rarity competes in
    GuaranteedPerPack int     // cards of this rarity guaranteed per pack (0 for chase slots)
    PullRatePerPack   float64 // probability of hitting this rarity in the chase slot
}

type PullRates struct {
    SetID        string
    PacksPerBox  int
    CardsPerPack int
    Rarities     []RarityRate
}
```

**Rarity string constants** (used across packages — no typed constants exist yet, treated as plain strings):

| Value | Description |
|---|---|
| `"common"` | Common card |
| `"uncommon"` | Uncommon card |
| `"rare"` | Rare holo |
| `"double_rare"` | Double Rare (ex card, base numbered) |
| `"ultra_rare"` | Ultra Rare (full art, numbered 223–242 in sv01) |
| `"illustration_rare"` | Illustration Rare (numbered 199–222 in sv01) |
| `"special_illustration_rare"` | Special Illustration Rare (numbered 243–252 in sv01) |
| `"hyper_rare"` | Hyper Rare / Gold (numbered 253–258 in sv01) |

### Functions

```go
func LoadContents(path string) (*SetContents, error)
func LoadPullRates(path string) (*PullRates, error)
```

---

## `internal/ev`

**File:** `internal/ev/ev.go`
**Responsibility:** EV calculation logic and `ev_history.json` persistence.

### Types

```go
// GradedPrices holds prices for graded copies of a card.
// All fields are pointers; absent data serialises as omitted JSON.
//
// PSA10 and Grade9 are populated for EVERY historical month (from chart_data).
// TAG10 through CGC10Pristine are populated for the MOST RECENT month only.
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

// CardPrice is the ungraded price of one card in one month.
// GradedPrices is only set for illustration_rare, special_illustration_rare,
// and hyper_rare cards.
type CardPrice struct {
    Number       string
    Name         string
    Rarity       string
    PriceUSD     float64       // ungraded market price
    GradedPrices *GradedPrices // nil for double_rare and ultra_rare
}

type MonthEV struct {
    Month      string      // "YYYY-MM"
    CardPrices []CardPrice // raw card prices for that month
    EV         float64     // expected value of one booster box
    SetValue   float64     // sum of all tracked card prices
    Top5Value  float64     // sum of the 5 most expensive card prices
    Top5Ratio  float64     // Top5Value / SetValue
}

type History struct {
    SetID  string    // e.g. "sv01"
    Months []MonthEV // sorted chronologically by Month
}
```

### Functions

```go
// Calculate computes EV, SetValue, Top5Value, Top5Ratio for one month.
// EV formula: Σ (pull_rate_per_pack × packs_per_box × avg_ungraded_price_for_rarity)
// Cards missing a price for a month are excluded from their rarity's average.
func Calculate(pr *setdata.PullRates, cardPrices []CardPrice) MonthEV

// LoadHistory reads ev_history.json. Returns an empty History (not an error)
// if the file does not exist.
func LoadHistory(path string) (*History, error)

// SaveHistory writes ev_history.json as indented JSON.
func SaveHistory(path string, h *History) error

// Lookup returns the MonthEV whose Month matches the calendar month of t.
func (h *History) Lookup(t time.Time) (MonthEV, bool)

// Upsert inserts or replaces the MonthEV for its month, keeping Months sorted.
func (h *History) Upsert(m MonthEV)
```

---

## `internal/pricecharting`

**File:** `internal/pricecharting/pricecharting.go`
**Responsibility:** Scrape price data from PriceCharting.com. Handles 429 rate limiting automatically.

### How PriceCharting stores data

Each product/card page embeds two data sources:

**1. JavaScript chart object** (historical monthly prices):
```js
VGPC.chart_data = { "used": [[timestampMs, priceCents], ...], "manualonly": [...], ... }
```

| `chart_data` key | Meaning |
|---|---|
| `"used"` | Ungraded price (cards) or main sealed price (products) |
| `"new"` / `"boxonly"` | Video game conditions; fallback for sealed products |
| `"manualonly"` | PSA 10 monthly history (manually curated) |
| `"graded"` | Grade 9 monthly history |

Verified by cross-referencing the most recent `"manualonly"` and `"graded"` values
against the Full Price Guide table's PSA 10 and Grade 9 prices.

**2. Full Price Guide table** (current prices, bottom of page):

HTML: `<table id="full-prices">` with `.title` cells (condition name) and `.price` cells (current USD price). The scraper normalises condition names case-insensitively and maps them to internal keys (`"psa_10"`, `"sgc_10"`, etc.) via `fullPriceKeyMap`.

### Types

```go
type MonthlyPrice struct {
    SnapshotDate time.Time // always the 15th of the month at UTC midnight
    PriceUSD     float64
}

// CardGradedHistory holds all data extracted from one IR/SIR/HR card page fetch:
// three monthly price series from chart_data, plus current prices from the
// Full Price Guide table.
type CardGradedHistory struct {
    Ungraded     []MonthlyPrice     // from "used" chart key
    PSA10        []MonthlyPrice     // from "graded" chart key; nil if not tracked
    Grade9       []MonthlyPrice     // from "grade9" chart key; nil if not tracked
    CurrentGuide map[string]float64 // from Full Price Guide table; keys are internal
                                   // names e.g. "psa_10", "sgc_10", "bgs_10_black_label"
}
```

### Functions

```go
// Scrape returns monthly ungraded price history for a product or card page.
// Used for: sealed products, double_rare, ultra_rare cards.
func Scrape(url string) ([]MonthlyPrice, error)

// ScrapeCardGradedHistory fetches one page and returns monthly histories for
// ungraded (chart_data["used"]), PSA10 (chart_data["graded"]), and Grade9
// (chart_data["grade9"]), plus current prices from the Full Price Guide table
// — all from a single HTTP request.
// Used for: illustration_rare, special_illustration_rare, hyper_rare cards.
func ScrapeCardGradedHistory(url string) (CardGradedHistory, error)

// ScrapeSoldLast30Days counts sold listings in the last 30 days.
// Extrapolates if fewer than 30 days of data are visible.
func ScrapeSoldLast30Days(url string) (float64, error)
```

### 429 Handling

`fetchPage` (internal) retries up to 5 times on HTTP 429. It reads the `Retry-After` response header:
- If an integer: waits that many seconds.
- If an HTTP date: waits until that time.
- If absent: defaults to 60 seconds.

---

## `internal/tcgplayer`

**File:** `internal/tcgplayer/tcgplayer.go`
**Responsibility:** Scrape current listing metrics from TCGPlayer. Requires Chrome/Chromium.

TCGPlayer is a React SPA; listing counts require a headless browser. The median ask price comes from a public REST API endpoint (`mpapi.tcgplayer.com`) and does not require a browser.

### Types

```go
type CurrentMetrics struct {
    MedianAskPrice float64 // listedMedianPrice for "Normal" condition
    ProductCount   int     // total active listings
    SellerCount    int     // unique sellers
}
```

### Functions

```go
// ScrapeCurrentMetrics fetches all three metrics for the given product URL.
// Only call this for the most-recent-month row.
func ScrapeCurrentMetrics(tcgPlayerURL string) (CurrentMetrics, error)
```

**Implementation detail:** extracts the product ID from the URL path (`/product/{id}/`), calls `mpapi.tcgplayer.com/v2/product/{id}/pricepoints` for the median price, then launches headless Chrome to read product/seller counts from the rendered DOM.

---

## `internal/sheets`

**File:** `internal/sheets/sheets.go`
**Responsibility:** Thin wrapper around the Google Sheets v4 API.

Authentication uses a Google service account credentials file. The service account must be granted **Editor** access to the spreadsheet.

### Types

```go
type Client struct { /* opaque */ }
```

### Functions

```go
func NewClient(ctx context.Context, credentialsFile, spreadsheetID string) (*Client, error)

// UpdateHeader overwrites row 1 with the given column names.
func (c *Client) UpdateHeader(sheetName string, headers []interface{}) error

// ReadRows returns all data rows (A2 onwards) as [][]string.
func (c *Client) ReadRows(sheetName string) ([][]string, error)

// AppendRows appends rows after the last existing data row.
func (c *Client) AppendRows(sheetName string, rows [][]interface{}) error

// WriteAllRows clears A2:Z then writes all rows from A2 in one API call.
func (c *Client) WriteAllRows(sheetName string, rows [][]interface{}) error
```
