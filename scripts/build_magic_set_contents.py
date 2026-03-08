#!/usr/bin/env python3
"""
Builds data/magic/set_contents.json from Scryfall API data.

Covers play-booster contents for the sets we have products for:
  Standard:          mkm, otj, mh3, blb, dsk, fdn
  Universes Beyond:  acr

Special slot cards (special_guest, breaking_news_*, retro_frame_*, etc.)
are fetched from their respective bonus-sheet set codes.

pricecharting_url is left empty — fill in separately.
"""

import json
import subprocess
import sys
import time
import urllib.parse


def scryfall_search(query: str) -> list[dict]:
    """Fetch all pages of a Scryfall card search and return the data list."""
    url = (
        "https://api.scryfall.com/cards/search"
        "?q=" + urllib.parse.quote(query) +
        "&order=collector_number"
    )
    cards = []
    while url:
        result = subprocess.run(
            ["curl", "-s", "-H", "User-Agent: FutureGadgetResearch/1.0", url],
            capture_output=True  # bytes mode to handle unicode in card names
        )
        data = json.loads(result.stdout.decode("utf-8"))
        if "data" not in data:
            print(f"  WARNING: no results for query: {query!r}", file=sys.stderr)
            print(f"  Response: {data.get('details', data)}", file=sys.stderr)
            return []
        cards.extend(data["data"])
        url = data.get("next_page")
        if url:
            time.sleep(0.11)
    return cards


def card_entry(c: dict, rarity_override: str | None = None) -> dict:
    return {
        "number": c["collector_number"],
        "name": c["name"],
        "rarity": rarity_override or c["rarity"],
        "pricecharting_url": "",
    }


# Standard filter for base (non-special-treatment) cards within a set.
# MH3 additionally needs -is:retro since retro frames are a separate slot.
BASE_FILTER_DEFAULT = "game:paper -is:promo -is:showcase -is:extendedart -is:borderless -is:fullart"
BASE_FILTER_MH3 = BASE_FILTER_DEFAULT + " -is:retro"


def fetch_base_rares_mythics(set_code: str) -> list[dict]:
    """Base-frame rares and mythics from the main set (no special treatments)."""
    extra = BASE_FILTER_MH3 if set_code == "mh3" else BASE_FILTER_DEFAULT
    q = f"set:{set_code} (rarity:rare OR rarity:mythic) {extra}"
    return [card_entry(c) for c in scryfall_search(q)]


def fetch_spg_range(collector_range: range, rarity_override: str = "special_guest") -> list[dict]:
    """
    Special Guest cards from the SPG bonus sheet by collector number range.
    All cards in the range are treated as 'special_guest' regardless of their
    Scryfall rarity within SPG (some are listed uncommon/rare/mythic).
    """
    lo, hi = min(collector_range), max(collector_range)
    q = f"set:spg cn>={lo} cn<={hi} game:paper"
    return [card_entry(c, rarity_override=rarity_override) for c in scryfall_search(q)]


def fetch_bonus_sheet(set_code: str, rarity_map: dict[str, str]) -> list[dict]:
    """
    Fetch cards from a bonus-sheet set (e.g. OTP for Breaking News).
    No treatment filters applied — bonus sheet cards often ARE the special treatment.
    """
    rarities = " OR ".join(f"rarity:{r}" for r in rarity_map)
    q = f"set:{set_code} ({rarities}) game:paper -is:promo"
    result = []
    for c in scryfall_search(q):
        internal = rarity_map.get(c["rarity"])
        if internal:
            result.append(card_entry(c, rarity_override=internal))
    return result


# ---------------------------------------------------------------------------
# Special Guest SPG collector-number ranges by set (2024 onwards)
# ---------------------------------------------------------------------------
SPG_RANGES = {
    "mkm": range(1, 11),    # SPG  1-10
    "otj": range(11, 21),   # SPG 11-20
    "mh3": None,            # no SPG slot
    "blb": range(21, 31),   # SPG 21-30
    "dsk": range(31, 41),   # SPG 31-40
    "fdn": None,            # no SPG slot
    "acr": None,            # Universes Beyond mini-set, no SPG
}

# ---------------------------------------------------------------------------
# Bonus sheets per set  {set_code -> [{bonus_set, rarity_map}]}
# ---------------------------------------------------------------------------
BONUS_SHEETS = {
    "otj": [
        {
            "set_code": "otp",  # Breaking News
            "rarity_map": {
                "rare": "breaking_news_rare",
                "mythic": "breaking_news_mythic",
            },
        }
    ],
}

SETS = {
    "Standard": ["mkm", "otj", "mh3", "blb", "dsk", "fdn"],
    "Universes Beyond": ["acr"],
}


def build_set(set_code: str) -> list[dict]:
    print(f"  Fetching base rares/mythics...")
    cards = fetch_base_rares_mythics(set_code)
    print(f"    {len(cards)} base cards")

    spg_range = SPG_RANGES.get(set_code)
    if spg_range:
        print(f"  Fetching SPG {min(spg_range)}-{max(spg_range)} (special guests)...")
        cards += fetch_spg_range(spg_range)

    for bs in BONUS_SHEETS.get(set_code, []):
        print(f"  Fetching bonus sheet {bs['set_code'].upper()}...")
        bonus = fetch_bonus_sheet(bs["set_code"], bs["rarity_map"])
        cards += bonus
        print(f"    {len(bonus)} bonus sheet cards")

    if set_code == "mh3":
        # MH3 retro frame cards occupy a dedicated play booster slot
        print("  Fetching MH3 retro frame cards...")
        q = "set:mh3 (rarity:rare OR rarity:mythic) is:retro game:paper -is:promo"
        retro = scryfall_search(q)
        for c in retro:
            internal = "retro_frame_mythic" if c["rarity"] == "mythic" else "retro_frame_rare"
            cards.append(card_entry(c, rarity_override=internal))
        print(f"    {len(retro)} retro frame cards")

    # Deduplicate by (number, name, rarity)
    seen = set()
    deduped = []
    for c in cards:
        key = (c["number"], c["name"], c["rarity"])
        if key not in seen:
            seen.add(key)
            deduped.append(c)

    return deduped


def main():
    output = {}
    for era, set_codes in SETS.items():
        output[era] = {}
        for code in set_codes:
            print(f"\n[{era}] {code.upper()}")
            cards = build_set(code)
            output[era][code] = {"cards": cards}

            by_rarity = {}
            for c in cards:
                by_rarity.setdefault(c["rarity"], 0)
                by_rarity[c["rarity"]] += 1
            print(f"  TOTAL: {len(cards)} cards -> {by_rarity}")

    out_path = "data/magic/set_contents.json"
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(output, f, indent=2, ensure_ascii=False)
    print(f"\nWrote {out_path}")


if __name__ == "__main__":
    main()
