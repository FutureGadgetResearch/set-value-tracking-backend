# products_all.json — Remaining TODOs

## 1. Fix broken PriceCharting slugs

The EX era slugs using `pokemon-ex-ruby-&-sapphire` style do **not** resolve to product pages —
they return a generic list page. Need to find the correct slug for each EX set booster box/pack.

Sets to verify and fix:
- `ex1` — EX Ruby & Sapphire (`pokemon-ex-ruby-&-sapphire`? try `pokemon-ex-ruby-sapphire` or browse pricecharting.com)
- `ex2` — EX Sandstorm
- `ex3` — EX Dragon
- `ex4` — EX Team Magma vs Team Aqua
- `ex5` — EX Hidden Legends
- `ex6` — EX FireRed & LeafGreen
- `ex7` — EX Team Rocket Returns
- `ex8` — EX Deoxys
- `ex9` — EX Emerald
- `ex10` — EX Unseen Forces
- `ex11` — EX Delta Species
- `ex12` — EX Legend Maker
- `ex13` — EX Holon Phantoms
- `ex14` — EX Crystal Guardians
- `ex15` — EX Dragon Frontiers
- `ex16` — EX Power Keepers

To find the right slug: go to https://www.pricecharting.com, search for e.g. "EX Ruby Sapphire booster box",
and copy the URL slug from the product page.

Also verify these older-era slugs (they looked correct but worth double-checking):
- e-Card sets: `ecard1` (Expedition), `ecard2` (Aquapolis), `ecard3` (Skyridge)
- Team Rocket, Gym Heroes, Gym Challenge — slugs used: `pokemon-team-rocket`, `pokemon-gym-heroes`, `pokemon-gym-challenge`
- Base Set 2: slug used `pokemon-base-set-2`

---

## 2. Add missing TCGPlayer IDs

All sets below have `"tcgplayer_id": ""`. Fill these in when available.
TCGPlayer product pages can be found at https://www.tcgplayer.com — search for the set name + product type,
then copy the numeric ID from the URL (e.g. `/product/206027/...` → `"206027"`).

### HeartGold & SoulSilver era
- `hgss1` HeartGold & SoulSilver — BB, BP
- `hgss2` Unleashed — BB, BP
- `hgss3` Undaunted — BB, BP
- `hgss4` Triumphant — BB, BP
- `col1` Call of Legends — BB, BP

### Platinum era
- `pl1` Platinum — BB, BP
- `pl2` Rising Rivals — BB, BP
- `pl3` Supreme Victors — BB, BP
- `pl4` Arceus — BB, BP

### Diamond & Pearl era
- `dp1` Diamond & Pearl — BB, BP
- `dp2` Mysterious Treasures — BB, BP
- `dp3` Secret Wonders — BB, BP
- `dp4` Great Encounters — BB, BP
- `dp5` Majestic Dawn — BB, BP
- `dp6` Legends Awakened — BB, BP
- `dp7` Stormfront — BB, BP

### EX era (all 16 sets) — BB and BP
- `ex1`–`ex16` (see set list above)

### Neo era
- `neo1` Neo Genesis — BB, BP
- `neo2` Neo Discovery — BB, BP
- `neo3` Neo Revelation — BB, BP
- `neo4` Neo Destiny — BB, BP

### Base Set era
- `base1` Base Set — BB, BP
- `jungle` Jungle — BB, BP
- `fossil` Fossil — BB, BP
- `base2` Base Set 2 — BB, BP
- `teamrocket` Team Rocket — BB, BP
- `gymheroes` Gym Heroes — BB, BP
- `gymchallenge` Gym Challenge — BB, BP
- `ecard1` Expedition — BB, BP
- `ecard2` Aquapolis — BB, BP
- `ecard3` Skyridge — BB, BP

### Black & White era (partial)
- `bw2` Emerging Powers — booster-box (BB only; BP ID 98549 already set)
- `bw3` Noble Victories — booster-pack
- `bw4` Next Destinies — booster-pack
- `bw5` Dark Explorers — booster-pack
- `bw6` Dragons Exalted — booster-pack
- `bw7` Boundaries Crossed — booster-box AND booster-pack (both empty)
- `bw8` Plasma Storm — booster-pack
- `bw9` Plasma Freeze — booster-pack
- `bw10` Plasma Blast — booster-pack

### XY era (partial)
- `xy5` Primal Clash — booster-pack
- `xy8` Breakthrough — elite-trainer-box
- `xy9` Breakpoint — booster-pack
- `xy12` Evolutions — booster-pack

---

## 3. Add missing product SKUs

These SKUs existed for some sets but weren't added (no ID found at time of writing):

### Black & White era
- ETBs existed for several BW sets but IDs were not sourced. Check:
  - `bw3` Noble Victories ETB, `bw4` Next Destinies ETB, `bw5` Dark Explorers ETB,
    `bw6` Dragons Exalted ETB, `bw8` Plasma Storm ETB, `bw9` Plasma Freeze ETB,
    `bw10` Plasma Blast ETB, `bw11` Legendary Treasures ETB

### XY era
- `xy5` Primal Clash had two ETBs (Kyogre + Groudon) — currently only Kyogre ETB tracked (one-ETB policy)
- `xy8` Breakthrough — ETB ID not sourced
- `xy10` Fates Collide — BP ID 168114 set, but verify
- `xy11` Steam Siege — BP ID 130013 set, but verify

### Sword & Shield era
- `swsh07` Evolving Skies — second ETB (Flareon/Jolteon, ID 242434) intentionally omitted
  due to dedup constraint (rowKey = snapshot_date + set_id + product_type). If dedup is ever
  changed to include product name/variant, this could be added back.

---

## 4. Verify standard_legal_until dates

Rotation dates used (approximate — verify against official Pokémon rotation announcements):
- SM era: `2022-04-01`
- SWSH era: sets rotated in waves; swsh01–04 = `2023-04-21`, swsh05–09 = `2024-04-05`
  (swsh07.5 and later still in extended depending on format)
- XY era: `2018-08-17`
- BW era: `2017-08-22`
- HGSS era: `2014-09-03`
- Platinum era: `2012-09-01`
- DP era: `2011-09-01`
- EX/Neo/Base Set: `2008-09-01` / `2004-09-01` (no longer standard legal, dates are historical)

---

## Notes

- Empty `tcgplayer_id: ""` is safe — the scraper logs a failure and continues.
- One product per `product_type` per set is enforced by the dedup key in `main.go`:
  `(snapshot_date, set_id, product_type)`. Adding two ETBs for the same set_id will cause
  only the first one to be treated as "new" on subsequent runs.
- PriceCharting URL format: `https://www.pricecharting.com/game/{game-slug}/{product-slug}`
- TCGPlayer ID: numeric string from the product URL path, e.g. `/product/206027/` → `"206027"`
