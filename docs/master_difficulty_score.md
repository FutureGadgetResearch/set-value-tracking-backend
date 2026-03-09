# Master Difficulty Score

## Purpose

The Master Difficulty Score (MDS) quantifies how hard it is to complete a rarity slot in a set — i.e., to collect at least one copy of every unique card at that rarity. It combines:

- **How rare the slot is** (pull rate per pack)
- **How many unique cards share that slot** (the coupon collector penalty)

A higher score means harder to complete.

---

## Formula

```
MDS = (n × H_n) / p
```

### Variables

| Variable | Name | Description |
|---|---|---|
| `n` | Unique Card Count | Number of distinct cards at this rarity in the set |
| `p` | Pull Rate per Pack | Probability of pulling *any* card of this rarity in a single pack |
| `H_n` | n-th Harmonic Number | Coupon collector penalty — accounts for duplicate pulls as the set nears completion |

### Harmonic Number

Exact:
```
H_n = 1 + 1/2 + 1/3 + ... + 1/n
```

Approximation (suitable for large n):
```
H_n ≈ ln(n) + 0.5772
```
where 0.5772 is the Euler–Mascheroni constant (γ).

### Interpretation of the Formula

The expected number of packs needed to collect all `n` unique cards from a rarity with pull rate `p` is derived from the coupon collector's problem:

```
E[packs] = n × H_n / p
```

The MDS *is* this expected pack count. It directly answers: **"On average, how many packs do you need to open to own at least one copy of every card at this rarity?"**

---

## Schema Addition

`master_difficulty_score` and `unique_card_count` are added to each rarity entry in `set_pull_rates.json`:

```json
{
  "rarity": "epic",
  "pull_rate_per_pack": 0.25,
  "unique_card_count": 42,
  "master_difficulty_score": 724.5
}
```

`unique_card_count` (`n`) must be populated from the corresponding `set_contents.json` entry.
`master_difficulty_score` is a derived/computed value and can be recalculated at any time.

---

## Worked Examples (Riftbound Origins — rb01)

Assumptions from `set_contents.json` and `set_pull_rates.json`:

| Rarity | n (unique cards) | p (pull rate) | H_n (approx) | MDS (packs) |
|---|---|---|---|---|
| Epic | 42 | 0.2500 | 4.315 | **724** |
| Alt Art | 30 | 0.0833 | 3.995 | **1,439** |
| Overnumbered | 12 | 0.0139 | 3.103 | **2,681** |
| Signature Overnumbered | 12 | 0.00139 | 3.103 | **26,810** |

### Step-by-step: Overnumbered (rb01)

```
n = 12
p = 0.0139
H_12 = 1 + 0.5 + 0.333 + 0.25 + 0.2 + 0.167 + 0.143 + 0.125 + 0.111 + 0.1 + 0.0909 + 0.0833
     = 3.103

MDS = (12 × 3.103) / 0.0139
    = 37.24 / 0.0139
    ≈ 2,681 packs
```

---

## Notes & Caveats

- **Assumes random, uniform distribution** within a rarity slot. If some cards are rarer than others within the same rarity (e.g., secret rares within a rarity tier), the formula underestimates difficulty.
- **Does not account for set printing ratios** beyond what `p` captures. If short prints exist at the same rarity label, treat them as a separate rarity.
- **`p > 1` is valid** (e.g., `rare` at 1.75 means ~1.75 rares per pack on average). The formula still holds; MDS will be less than `n × H_n`.
- **MDS is in units of packs**, not boxes or dollars. Convert using pack counts per box and pack MSRP as needed.
