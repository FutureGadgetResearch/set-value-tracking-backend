# Google Sheet Schema

The sheet is named `product_tracking`. Row 1 is the header (managed manually or via `sheets.UpdateHeader`). Data starts at row 2.

Each row represents one **product** (e.g. a booster box) in one **calendar month**.

---

## Column Reference

| Column | Type | Source | Description |
|---|---|---|---|
| `snapshot_date` | date string `YYYY-MM-15` | PriceCharting history | The 15th of the month this row represents |
| `tcg` | string | `products.json` | Trading card game, e.g. `"pokemon"` |
| `set_id` | string | `products.json` | Short set identifier, e.g. `"sv01"` |
| `era` | string | `products.json` | Series/era name, e.g. `"Scarlet & Violet"` |
| `release_date` | date string | `products.json` | Set release date |
| `is_special_set` | bool | `products.json` | Whether this is a promo/special set |
| `is_standard_legal` | bool | computed | `true` if `snapshot_date` is before `standard_legal_until` |
| `product_type` | string | `products.json` | e.g. `"booster-box"`, `"etb"` |
| `msrp` | float | `products.json` | Manufacturer suggested retail price (USD) |
| `market_price` | float | PriceCharting | Monthly market price of the sealed product (USD) |
| `ev` | float | `ev_history.json` | Expected value of one box based on card prices (USD) |
| `price_change_90d` | float | computed | `(price_now - price_90d_ago) / price_90d_ago`; empty if no 90-day-ago data |
| `seller_count` | int | TCGPlayer | Active unique sellers — **current month row only** |
| `product_count` | int | TCGPlayer | Active listings — **current month row only** |
| `avg_sold_30d` | float | PriceCharting | Avg sold listings per day over last 30 days — **current month row only** |
| `sales_to_inventory_ratio` | float | — | Not yet populated (reserved) |
| `median_ask_price` | float | TCGPlayer | Median listed ask for Normal condition — **current month row only** |
| `set_value` | float | `ev_history.json` | Sum of all tracked card prices for the month (USD) |
| `top_5_value` | float | `ev_history.json` | Sum of the 5 most expensive card prices (USD) |
| `top_5_ratio` | float | `ev_history.json` | `top_5_value / set_value` |
| `price_increase_next_90_days` | float | — | Not yet populated (reserved for ML predictions) |
| `price_increase_next_365_days` | float | — | Not yet populated (reserved) |
| `price_increase_next_2_years` | float | — | Not yet populated (reserved) |
| `price_increase_next_5_years` | float | — | Not yet populated (reserved) |

---

## Deduplication Key

Before writing, `main.go` reads all existing rows and builds a set of:

```
(snapshot_date, set_id, product_type)
```

Only rows whose key is absent from the sheet are appended. This makes all runs idempotent — re-running never creates duplicate rows.

**Implication:** If a column value needs to be corrected for an existing row, you must edit the sheet manually or clear and rewrite it using `sheets.WriteAllRows`.

---

## "Current month only" columns

Columns marked **current month row only** are populated by scraping live data (TCGPlayer, PriceCharting sold listings). They are set only on the **last** row in the per-product slice (the most recent `snapshot_date`). All historical rows leave these columns empty.

This is intentional: listing counts and sold velocity are meaningful only as current-state snapshots, not as time-series data that can be reconstructed from PriceCharting's historical chart.
