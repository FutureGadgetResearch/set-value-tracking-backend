# Data Files Reference

All data files live under `data/`. JSON files that are hand-maintained are described here with full field-level documentation. Generated files (like `ev_history.json`) are described structurally but should not be edited by hand.

---

## `data/products.json`

Defines the sealed products to track. One entry per product type per set (e.g., a set with both a booster box and an ETB would have two entries).

```jsonc
{
  "products": [
    {
      "set_id": "sv01",               // Short set identifier; must match the sets/ folder name
      "tcg": "pokemon",               // Trading card game name
      "era": "Scarlet & Violet",      // Series/era name
      "release_date": "2023-03-31",   // YYYY-MM-DD
      "is_special_set": false,        // True for promo/special sets
      "standard_legal_until": "2026-04-01", // YYYY-MM-DD; omit or "" if still legal
      "product_type": "booster-box",  // Identifies this row; e.g. "booster-box", "etb", "blister"
      "msrp": 143.64,                 // Manufacturer suggested retail price (USD)
      "pricecharting_url": "...",     // Full URL to the product's PriceCharting page
      "tcgplayer_url": "..."          // Full URL to the product's TCGPlayer listing page
    }
  ]
}
```

**Notes:**
- `set_id` is the join key between products and set data (`data/sets/{set_id}/`).
- `product_type` is used as part of the dedup key when writing to the sheet — changing it will create new rows.
- `standard_legal_until` controls the `is_standard_legal` column in the sheet. Empty = always legal.

---

## `data/sets/{set_id}/pull_rates.json`

Defines how many cards of each rarity appear per pack. Used by `ev.Calculate()`.

```jsonc
{
  "set_id": "sv01",
  "packs_per_box": 36,
  "cards_per_pack": 10,
  "rarities": [
    {
      "rarity": "common",         // Must match rarity values used in contents.json
      "slot": "common",           // Physical pack slot this rarity occupies
      "guaranteed_per_pack": 4,   // Cards of this rarity guaranteed per pack
      "pull_rate_per_pack": 1.0   // Probability of getting this rarity in the chase slot
                                  // (1.0 = guaranteed; <1.0 = probabilistic)
    },
    {
      "rarity": "double_rare",
      "slot": "rare",             // Competes in the "rare" chase slot
      "guaranteed_per_pack": 0,
      "pull_rate_per_pack": 0.1376  // ~13.76% chance per pack
    }
    // ... one entry per rarity tier
  ]
}
```

**EV formula context:**
```
expected_hits_per_box = pull_rate_per_pack × packs_per_box
rarity_ev = expected_hits_per_box × avg(ungraded prices of cards in that rarity)
total_ev = Σ rarity_ev  (summed across all tracked rarities)
```

Rarities with `guaranteed_per_pack > 0` (common, uncommon) contribute to EV if they have entries in `contents.json`. Currently these are excluded by design (bulk not tracked in contents.json).

**SV01 pull rates source:** Community-aggregated data from 1,728 packs (Card Shop Live). TPCi does not publish official pull rates.

---

## `data/sets/{set_id}/contents.json`

Maps every tracked card in a set to its rarity and PriceCharting URL. Only non-bulk cards are listed (double rare and above). This is the card universe used for EV, set value, and top-5 calculations.

```jsonc
{
  "set_id": "sv01",
  "cards": [
    {
      "number": "045",              // Zero-padded card number as printed on the card
      "name": "Gyarados ex",        // Card name exactly as it appears on PriceCharting
      "rarity": "double_rare",      // Must match a rarity in pull_rates.json
      "pricecharting_url": "https://www.pricecharting.com/game/pokemon-scarlet-&-violet/gyarados-ex-45"
    }
  ]
}
```

**PriceCharting URL pattern:**
```
https://www.pricecharting.com/game/pokemon-scarlet-&-violet/{name-slug}-{card-number}
```
- Name slug: lowercase, spaces → hyphens, special characters dropped.
- Card number: the actual printed number (not zero-padded in the URL).

**Graded price tracking:** Cards with `rarity` in `["illustration_rare", "special_illustration_rare", "hyper_rare"]` automatically get PSA 10 + Grade 9 historical prices and current TAG/ACE/SGC/CGC/BGS prices scraped by `cmd/evbackfill`. No extra fields needed here.

**SV01 card counts by rarity:**
| Rarity | Count | Number range |
|---|---|---|
| `double_rare` | 12 | 019–158 |
| `ultra_rare` | 20 | 223–242 |
| `illustration_rare` | 24 | 199–222 |
| `special_illustration_rare` | 10 | 243–252 |
| `hyper_rare` | 6 | 253–258 |
| **Total** | **72** | |

---

## `data/sets/{set_id}/ev_history.json` *(generated)*

Written by `cmd/evbackfill`. Do not edit by hand. Safe to delete and regenerate with a full backfill run.

```jsonc
{
  "set_id": "sv01",
  "months": [
    {
      "month": "2023-03",           // "YYYY-MM" — always the calendar month, not a specific day
      "card_prices": [
        {
          "number": "045",
          "name": "Gyarados ex",
          "rarity": "double_rare",
          "price_usd": 4.50,        // Ungraded market price for this month
          "graded_prices": {        // Only present for IR/SIR/HR cards
            "psa_10": 28.00,        // Monthly historical — present for all months
            "grade_9": 12.00,       // Monthly historical — present for all months
            "tag_10": 31.00,        // Current month only
            "ace_10": 29.50,        // Current month only
            "sgc_10": 27.00,        // Current month only
            "cgc_10": 26.50,        // Current month only
            "bgs_10": 30.00,        // Current month only
            "bgs_10_black_label": 95.00,  // Current month only
            "cgc_10_pristine": 110.00     // Current month only
          }
        }
        // ... one entry per card that had a price that month
      ],
      "ev": 142.50,           // Expected value of one booster box (USD)
      "set_value": 850.25,    // Sum of all tracked ungraded card prices
      "top_5_value": 320.10,  // Sum of the 5 highest ungraded card prices
      "top_5_ratio": 0.376    // top_5_value / set_value
    }
  ]
}
```

**Regeneration:** Re-running `cmd/evbackfill` is safe at any time. It loads the existing file first and upserts (not duplicates) each month. Months that already exist are fully overwritten with fresh data.
