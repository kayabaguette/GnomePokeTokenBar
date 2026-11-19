#!/usr/bin/python3 -I
"""Protocol stub standing in for bin/ptb-helper in sandbox smoke tests.

Installed as <ext>/bin/ptb-helper by extension/test/prepare-sandbox.sh. One
process, no threads, no children: stdin is polled with select() so the
headless script's helper count stays exact and EOF ends the process.

Behaviour (docs/protocol.md):
  - prints hello, then the fixture state (fromCache: true) with every
    timestamp rebased to now and the menu-subject sprite pointed at the
    staged sprite cache;
  - answers every command id: ping/wake/networkUp {}, setSettings (merges
    into the applied set, clamps both difficulties, accepts the optional
    shopDifficulty, state first), refresh (refreshing state, fresh state,
    result), getSprite (staged sprites), getDexPage / getCatchLog /
    getSpecies / setRepresentative (from fixtures/dex-page.json,
    catch-log.json and species.json, filtered, sorted and paged here),
    anything else unknownCommand;
  - Milestone 2 (protocol §5.4, §7.4, §7.5), all against an in-memory copy
    of the state; every mutation runs derive() (shop, bag, egg numbers),
    emits the new state, then any evolve/graduate event, then the result:
      buyItem / buyEgg   deduct the wallet, change inventory or release the
                         active Pokémon into a new egg (catch log updated);
                         insufficientFunds, alreadyOwned, noActive, badArgs
      planCandies        real plan from the stage costs (matches
                         fixtures/candy-plan.json for count 3)
      useCandy           consumes candies, evolves or graduates (events,
                         celebration window cleared after 4 or 6 s);
                         noActive, noStock, notReady, badArgs
      useMint            new random nature (seed 7), one Mint less
      getRecap           computed from usage.combined.heatmap, the catch log
                         and the applied weekStart (week/month/year, any
                         offset in -1000..0; canGoBack turns false when the
                         previous period predates heatmap.first)
    Candy and shop use the same formulas as the helper (bag-shop.md §1-§2);
    the stub only has one species line, so an evolution just walks evoLine;
  - Milestone 3 (protocol §7.6, §7.7, §8.1), against the same in-memory state:
      pray               egg only (notEgg while a Pokémon is active): bumps
                         companion.egg.prayers, emits the state, replies
                         {count}. The start value comes from the fixture or
                         the `eggPrayers` config key
      getSprite {item}   badArgs for a name outside [a-z0-9-]+; `mint` is
                         unavailable (no upstream sprite); else the staged
                         item-<name>.png if there is one, falling back to the
                         staged egg.png as a stand-in, else unavailable
      exportSave         validates the absolute path and replies from
                         fixtures/export-save.json with the path echoed. It
                         never writes a file
      inspectSave        replies from fixtures/inspect-save.json
      importSave         needs the fixture digest (else fileChanged), emits
                         the state, replies {summary}
      listSnapshots / createSnapshot / restoreSnapshot
                         an in-memory list seeded from
                         fixtures/list-snapshots.json (max 10, newest first);
                         restore takes a safety snapshot, notFound for an
                         unknown id
    Error injection for the preferences window: a path whose file name
    contains `notasave`, `newer`, `toolarge`, `missing`, `denied`, `nospace`
    or `changed` answers notASaveFile, newerSchema, fileTooLarge,
    fileNotFound, permissionDenied, noSpace or fileChanged.
  - emits each line of fixtures/events.ndjson after `eventsAfter` seconds,
    `eventGap` seconds apart (negative eventsAfter disables events);
  - exits 0 on stdin EOF.

Test hooks: `stubSilent` never answers; `stubExit` exits with status 1.
Environment overrides: PTB_STUB_PROTOCOL, PTB_STUB_EVENTS_AFTER.

Config: <ext>/test/stub-config.json, e.g.
  {"fixture": "state-active.json", "eventsAfter": 6, "eventGap": 1, "protocol": 1,
   "dexPad": 30, "speciesDelay": 0, "wallet": 800000000,
   "inventory": {"shinyCharm": 1, "mint": 0}, "shopDifficulty": 0.5, "eggPrayers": 37}
  dexPad adds that many synthetic owned species (and one graduation each) so
  the grid and the log have several pages; speciesDelay delays getSpecies
  replies by that many seconds (to keep a command pending across a disable).
  wallet, inventory (merged over the fixture's counts) and shopDifficulty
  override the fixture so other Shop and Bag states can be rendered, for
  example the owned Charm (state is then derive()d again). eggPrayers sets
  the egg's prayer count when the fixture shows an egg.

The dex answers only mimic the helper (helper/internal/companion/views.go):
the fixtures, not this file, are the contract.
"""

import json
import math
import os
import random
import re
import select
import shutil
import sys
import time
from datetime import date, datetime, timedelta, timezone

EXT = os.path.dirname(os.path.dirname(os.path.realpath(__file__)))
TEST = os.path.join(EXT, "test")
TS_RE = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")
STAGED = ["stub-25", "stub-6", "stub-94"]
ITEM_NAME_RE = re.compile(r"^[a-z0-9-]+$")  # sprites.validName
SNAP_ID_RE = re.compile(r"^companion-snapshot-[0-9-]+\.json$")
FILE_ERRORS = [("notasave", "notASaveFile", "not a PokeTokenBar save file"),
               ("newer", "newerSchema", "made by a newer version (schema 3, supported 2)"),
               ("toolarge", "fileTooLarge", "file is larger than 8 MiB"),
               ("missing", "fileNotFound", "no such file"),
               ("denied", "permissionDenied", "permission denied"),
               ("nospace", "noSpace", "no space left on device")]


def log(msg):
    print(f"ptb-helper: stub: {msg}", file=sys.stderr, flush=True)


def write(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def load_config():
    path = os.path.join(TEST, "stub-config.json")
    cfg = {"fixture": "state-active.json", "eventsAfter": 6, "eventGap": 1.0, "protocol": 1}
    if os.path.exists(path):
        with open(path, encoding="utf-8") as f:
            cfg.update(json.load(f))
    # Overrides for the helperClient unit test (test/helperClient.test.js).
    if os.environ.get("PTB_STUB_PROTOCOL"):
        cfg["protocol"] = int(os.environ["PTB_STUB_PROTOCOL"])
    if os.environ.get("PTB_STUB_EVENTS_AFTER"):
        cfg["eventsAfter"] = float(os.environ["PTB_STUB_EVENTS_AFTER"])
    return cfg


def parse_ts(s):
    return datetime.strptime(s, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def fmt_ts(dt):
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def rebase(node, delta):
    """Shift every RFC 3339 timestamp by delta so countdowns stay realistic."""
    if isinstance(node, dict):
        return {k: rebase(v, delta) for k, v in node.items()}
    if isinstance(node, list):
        return [rebase(v, delta) for v in node]
    if isinstance(node, str) and TS_RE.match(node):
        return fmt_ts(parse_ts(node) + delta)
    return node


def sprites_dir():
    base = os.environ.get("PTB_STATE_DIR")
    if not base:
        base = os.path.join(os.environ.get("XDG_CACHE_HOME", os.path.expanduser("~/.cache")), "poketokenbar-stub")
    path = os.path.join(base, "sprites")
    os.makedirs(path, exist_ok=True)
    return path


def stage_sprites():
    """Copy the generated sprite fixture into the sandbox cache dir."""
    src = os.path.join(TEST, "sprite-fixture")
    dst = sprites_dir()
    if not os.path.isdir(src):
        log(f"no sprite fixture at {src}; sprites stay 'loading'")
        return dst
    for name in os.listdir(src):
        s = os.path.join(src, name)
        d = os.path.join(dst, name)
        if os.path.isdir(s):
            shutil.copytree(s, d, dirs_exist_ok=True)
        else:
            shutil.copy2(s, d)
    return dst


def sprite_reply(req, base):
    if req.get("egg"):
        path = os.path.join(base, "egg.png")
        return {"key": "egg", "status": "ready", "manifestPath": path} if os.path.exists(path) else None
    if "item" in req:  # protocol §8.1: egg, then item, then speciesId
        name = req["item"]
        if not isinstance(name, str) or not ITEM_NAME_RE.match(name):
            return "badArgs"
        if name == "mint":
            return None
        for candidate in (f"item-{name}.png", "egg.png"):
            path = os.path.join(base, candidate)
            if os.path.exists(path):
                return {"key": f"item-{name}", "status": "ready", "manifestPath": path}
        return None
    sid = req.get("speciesId")
    if not isinstance(sid, int) or not 1 <= sid <= 649:
        return "badArgs"
    staged = os.path.join(base, STAGED[sid % len(STAGED)])
    if not os.path.isdir(staged):
        return None
    form = req.get("form")
    asset = f"{sid}-{form}" if form else str(sid)
    key = asset + ("-back" if req.get("back") else "") + "-" + ("sh" if req.get("shiny") else "") + \
        ("a" if req.get("animated", True) else "s")
    if req.get("animated", True):
        return {"key": key, "status": "ready", "manifestPath": os.path.join(staged, "manifest.json")}
    return {"key": key, "status": "ready", "manifestPath": os.path.join(staged, "frame-000.png")}


# ---------------------------------------------------------------------------
# Milestone 2: Shop, Bag, recap (docs/protocol.md §5.4, §7.4, §7.5). Pure
# functions over the state dict; the fixtures, not this file, are the contract.
# ---------------------------------------------------------------------------

try:
    from zoneinfo import ZoneInfo
    LOCAL_TZ = ZoneInfo("Europe/Paris")  # the fixtures' zone (protocol §13)
except Exception:  # no tzdata: the fixture date is UTC+1 anyway
    LOCAL_TZ = timezone(timedelta(hours=1))

ITEMS = ["rareCandy", "mint", "shinyCharm"]  # ItemKind.allCases order
ITEM_BASE = {"rareCandy": 500_000_000, "mint": 100_000_000, "shinyCharm": 3_000_000_000}
EGG_BASE = {None: 1_000_000_000, "uncommon": 2_500_000_000, "rare": 4_000_000_000}
GRAD_TOTAL = {"common": 750_000_000, "uncommon": 1_875_000_000, "rare": 3_000_000_000, "legendary": 6_000_000_000}
CANDY_XP = 100_000_000
NATURES = ["hardy", "lonely", "brave", "adamant", "naughty", "bold", "docile", "relaxed", "impish", "lax", "timid",
           "hasty", "serious", "jolly", "naive", "modest", "mild", "quiet", "bashful", "rash", "calm", "gentle",
           "sassy", "careful", "quirky"]


def is_int(v):
    return isinstance(v, int) and not isinstance(v, bool)


def rnd(x):
    """Round half away from zero (Swift .rounded(), Go math.Round)."""
    return int(math.floor(x + 0.5)) if x >= 0 else -int(math.floor(-x + 0.5))


def clamp_difficulty(d):
    if not isinstance(d, (int, float)) or isinstance(d, bool) or d != d or d in (float("inf"), float("-inf")):
        return 1.0
    return min(2.0, max(0.1, float(d)))


def scaled(base, d):
    return rnd(base * clamp_difficulty(d))


def stage_costs(active, growth):
    """Effective cost of every remaining stage (bag-shop.md §2.2, companion.md §2.3)."""
    k, total = active["totalForms"], GRAD_TOTAL[active["rarity"]]
    boost = 2 if active["growthBoost"] else 1
    out = []
    for stage in range(active["stageIndex"], k):
        std = rnd(total * (stage + 1) / (k * (k + 1) / 2))
        out.append(scaled(max(1, rnd(std / boost)), growth))
    return out


def candy_max_use(active, stock, growth):
    remaining = max(0, sum(stage_costs(active, growth)) - active["usedAtStage"])
    return min(stock, -(-remaining // CANDY_XP))


def make_plan(active, max_use, count, growth):
    c = min(max(0, count), max_use)
    if c == 0 or active is None:
        return None
    rem, evolves = active["usedAtStage"] + c * CANDY_XP, False
    costs = stage_costs(active, growth)
    for i, cost in enumerate(costs):
        if rem < cost:
            break
        rem -= cost
        if i == len(costs) - 1:
            return {"count": c, "xp": c * CANDY_XP, "evolves": evolves, "graduates": True,
                    "carryoverXP": 0, "discardedXP": rem}
        evolves = True
    return {"count": c, "xp": c * CANDY_XP, "evolves": evolves, "graduates": False,
            "carryoverXP": rem if evolves else 0, "discardedXP": 0}


def derive(state):
    """Recompute everything that follows from wallet, inventory, phase and difficulty:
    shop, bag, and the egg/growth numbers. derive(fixture) must equal the fixture."""
    c = state["companion"]
    diff = state["settingsApplied"].get("shopDifficulty", 1.0)
    growth = state["settingsApplied"].get("growthDifficulty", 1.0)
    wallet, inv, active = c["wallet"], c["inventory"], c["active"]
    entries = []
    for item in ITEMS:
        price, passive, owned = scaled(ITEM_BASE[item], diff), item == "shinyCharm", inv.get(item, 0)
        purchased = passive and owned > 0
        can = not purchased and wallet >= price
        entries.append({"kind": "item", "item": item, "tier": None, "price": price, "passive": passive,
                        "owned": owned, "purchased": purchased, "canBuy": can,
                        "maxCount": 0 if not can or price <= 0 else (1 if passive else wallet // price)})
    for tier in (None, "uncommon", "rare"):
        price = scaled(EGG_BASE[tier], diff)
        can = active is not None and wallet >= price
        entries.append({"kind": "egg", "item": None, "tier": tier, "price": price, "passive": False, "owned": 0,
                        "purchased": False, "canBuy": can, "maxCount": 1 if can else 0})
    entries.sort(key=lambda e: (e["purchased"], e["price"]))  # stable, like sort.SliceStable
    c["shop"] = {"entries": entries, "shinyOdds": 48 if inv.get("shinyCharm", 0) > 0 else 64}
    if active is None:
        blocked, max_use = "egg", 0
    elif inv.get("rareCandy", 0) <= 0:
        blocked, max_use = "noStock", 0
    elif not active["evoLine"]:
        blocked, max_use = "lineLoading", 0
    else:
        blocked, max_use = None, candy_max_use(active, inv["rareCandy"], growth)
    c["bag"] = {"candyMaxUse": max_use, "canUseMint": active is not None and inv.get("mint", 0) > 0,
                "candyBlocked": blocked}
    if c["egg"] is not None:
        c["egg"]["imminent"] = c["egg"]["used"] * 10 >= c["egg"]["threshold"] * 9
        c["growthPercent"] = min(100, c["egg"]["used"] * 100 // max(1, c["egg"]["threshold"]))
    elif active is not None:
        c["growthPercent"] = min(100, active["usedAtStage"] * 100 // max(1, active["threshold"]))
    return state


def local_day(ts):
    return parse_ts(ts).astimezone(LOCAL_TZ).date()


def add_months(d, n):
    m = d.year * 12 + (d.month - 1) + n
    return d.replace(year=m // 12, month=m % 12 + 1, day=1)


def month_end(d):
    return add_months(d, 1) - timedelta(days=1)


def period_of(scope, offset, today, week_start):
    """First and last (inclusive) day of the calendar period `offset` periods before today's."""
    if scope == "week":
        start = today - timedelta(days=(today.isoweekday() - week_start) % 7) + timedelta(days=7 * offset)
        return start, start + timedelta(days=6)
    if scope == "month":
        start = add_months(today.replace(day=1), offset)
        return start, month_end(start)
    start = date(today.year + offset, 1, 1)
    return start, date(start.year, 12, 31)


def day_span(start, end):
    return [start + timedelta(days=i) for i in range((end - start).days + 1)]


def make_recap(state, log, scope, offset):
    """recap-heatmap.md §1.3 over the heatmap grid (the stub's whole ledger)."""
    hm = state["usage"]["combined"]["heatmap"]
    grid_start, first = date.fromisoformat(hm["start"]), date.fromisoformat(hm["first"])
    today = date.fromisoformat(state["usage"]["calendar"]["today"])
    week_start = state["settingsApplied"].get("weekStart", 1)

    def tokens(day):
        i = (day - grid_start).days
        return hm["tokens"][i] if 0 <= i < len(hm["tokens"]) and day <= today else 0

    def known(day):
        return tokens(day) > 0 or day >= first

    start, end = period_of(scope, offset, today, week_start)
    days = day_span(start, end)
    elapsed = [d for d in days if d <= today]
    if scope == "year":
        buckets = []
        for m in range(12):
            m_start = date(start.year, m + 1, 1)
            keys = [d for d in day_span(m_start, month_end(m_start)) if d <= today]
            buckets.append({"key": m_start.isoformat(), "tokens": sum(tokens(d) for d in keys),
                            "isCurrent": m_start <= today <= month_end(m_start),
                            "hasData": any(known(d) for d in keys)})
    else:
        buckets = [{"key": d.isoformat(), "tokens": tokens(d) if d <= today else 0, "isCurrent": d == today,
                    "hasData": d <= today and known(d)} for d in days]
    total = sum(tokens(d) for d in elapsed)
    best = None
    for d in elapsed:  # strict > keeps the earliest day on a tie
        if tokens(d) > 0 and (best is None or tokens(d) > tokens(best)):
            best = d
    streak = run = 0
    for d in elapsed:
        run = run + 1 if tokens(d) > 0 else 0
        streak = max(streak, run)
    in_progress = start <= today <= end
    prev_start, prev_end = period_of(scope, offset - 1, today, week_start)
    prev_days = day_span(prev_start, prev_end)
    compared = prev_days[:len(elapsed)] if in_progress else prev_days
    previous = sum(tokens(d) for d in compared) if compared and compared[0] >= first else None
    grads = [e for e in log if not e["isRaising"] and not e["released"] and e["caughtAt"]
             and start <= local_day(e["caughtAt"]) <= end]
    grads.sort(key=lambda e: e["caughtAt"], reverse=True)
    return {
        "scope": scope, "offset": offset, "start": start.isoformat(), "end": end.isoformat(), "total": total,
        "isInProgress": in_progress, "previousTotal": previous,
        "bestDay": {"key": best.isoformat(), "tokens": tokens(best)} if best else None,
        "activeDays": sum(1 for d in elapsed if tokens(d) > 0), "countedDays": sum(1 for d in elapsed if known(d)),
        "bestStreak": streak, "canGoBack": prev_days[-1] >= first, "buckets": buckets,
        "graduated": len(grads),
        "graduates": [{"entryId": e["id"], "speciesId": e["speciesId"], "form": e["form"], "isShiny": e["isShiny"],
                       "displayName": e["displayName"]} for e in grads[:4]]}


RARITY_RANK = {"legendary": 3, "rare": 2, "uncommon": 1, "common": 0}
REGIONS = [("kanto", 1, 151), ("johto", 152, 251), ("hoenn", 252, 386), ("sinnoh", 387, 493), ("unova", 494, 649)]


def region_of(sid):
    for key, lo, hi in REGIONS:
        if lo <= sid <= hi:
            return key
    return None


def load_fixture(name):
    with open(os.path.join(TEST, "fixtures", name), encoding="utf-8") as f:
        return json.load(f)["data"]


class Dex:
    """Owned species, catch log and species detail, built from the fixtures."""

    def __init__(self, state, pad):
        self.state = state
        page = load_fixture("dex-page.json")
        self.cells = [dict(c) for c in page["cells"]]
        self.log = [dict(e) for e in load_fixture("catch-log.json")["entries"]]
        self.species = load_fixture("species.json")
        have = {c["speciesId"] for c in self.cells}
        sid, added = 1, 0
        rarities = ["common", "uncommon", "rare", "legendary"]
        while added < pad and sid <= 649:
            if sid not in have:
                rarity = rarities[sid % 4]
                self.cells.append({
                    "speciesId": sid, "displayName": f"Species {sid}", "rarity": rarity, "isShiny": sid % 7 == 0,
                    "hasNormal": True, "isRaising": False, "isRepresentative": False, "forms": None,
                    "sprite": {"speciesId": sid, "form": None, "shiny": sid % 7 == 0, "back": False, "animated": False},
                    "spriteKey": f"{sid}-s"})
                self.log.append({
                    "id": f"pad-{sid}", "isRaising": False, "released": False, "rarity": rarity,
                    "isShiny": sid % 7 == 0, "nature": "hardy", "form": None,
                    "caughtAt": f"2026-09-{(sid % 28) + 1:02d}T10:00:00Z", "speciesId": sid,
                    "displayName": f"Species {sid}",
                    "chain": [{"speciesId": sid, "form": None, "displayName": f"Species {sid}"}]})
                added += 1
            sid += 1

    def rep(self):
        return self.state["companion"].get("representative")

    def cell(self, sid):
        return next((c for c in self.cells if c["speciesId"] == sid), None)

    @staticmethod
    def matches(query, ids, names):
        q = query.strip()
        if not q:
            return True
        try:
            n = int(q.lstrip("#"))
        except ValueError:
            n = None
        for i in ids:
            if (n is not None and i == n) or q in f"#{i}":
                return True
        return any(q.lower() in (name or "").lower() for name in names)

    @staticmethod
    def sort_key(sort):
        if sort == "numberDesc":
            return lambda c: -c["speciesId"]
        if sort in ("nameAsc", "nameDesc"):
            return lambda c: (c["displayName"].lower(), c["speciesId"])
        if sort == "rarityDesc":
            return lambda c: (-RARITY_RANK.get(c["rarity"], 0), c["speciesId"])
        return lambda c: c["speciesId"]

    def page(self, a):
        rep = self.rep() or {}
        rarity_counts = {"legendary": 0, "rare": 0, "uncommon": 0, "common": 0}
        regions = []
        for key, lo, hi in REGIONS:
            owned = [c for c in self.cells if lo <= c["speciesId"] <= hi]
            shiny = [c for c in owned if c["isShiny"]]
            total = hi - lo + 1
            regions.append({"key": key, "owned": len(owned), "shinyOwned": len(shiny), "total": total,
                            "complete": len(owned) >= total, "shinyComplete": len(shiny) >= total})
        out = []
        for c in self.cells:
            rarity_counts[c["rarity"]] = rarity_counts.get(c["rarity"], 0) + 1
            if a.get("rarity") and c["rarity"] != a["rarity"]:
                continue
            if a.get("region") and region_of(c["speciesId"]) != a["region"]:
                continue
            if a.get("shinyOnly") and not c["isShiny"]:
                continue
            if not self.matches(a.get("query", ""), [c["speciesId"]], [c["displayName"]]):
                continue
            out.append(dict(c, isRepresentative=c["speciesId"] == rep.get("speciesId")))
        sort = a.get("sort", "numberAsc")
        out.sort(key=self.sort_key(sort), reverse=sort == "nameDesc")
        size = a.get("pageSize", 16)
        count = max(1, (len(out) + size - 1) // size)
        page = min(max(1, a.get("page", 1)), count)
        anchor = a.get("anchorSpeciesId")
        if anchor is not None:
            for i, c in enumerate(out):
                if c["speciesId"] == anchor:
                    page = i // size + 1
        return {"page": page, "pageSize": size, "pageCount": count, "total": len(out),
                "cells": out[(page - 1) * size:page * size], "regions": regions, "rarityCounts": rarity_counts}

    def catch_log(self, a):
        counts = {"legendary": 0, "rare": 0, "uncommon": 0, "common": 0}
        out = []
        for e in self.log:
            counts[e["rarity"]] = counts.get(e["rarity"], 0) + 1
            if a.get("rarity") and e["rarity"] != a["rarity"]:
                continue
            if a.get("shinyOnly") and not e["isShiny"]:
                continue
            ids = [e["speciesId"]] + [n["speciesId"] for n in e["chain"]]
            if not self.matches(a.get("query", ""), ids, [n["displayName"] for n in e["chain"]]):
                continue
            out.append(e)
        sort = a.get("sort", "recentFirst")
        if sort in ("recentFirst", "oldestFirst"):
            dated = sorted([e for e in out if not e["isRaising"]], key=lambda e: e["caughtAt"] or "",
                           reverse=sort == "recentFirst")
            active = [e for e in out if e["isRaising"]]
            out = active + dated if sort == "recentFirst" else dated + active
        else:
            out.sort(key=self.sort_key(sort), reverse=sort == "nameDesc")
        off, lim = a.get("offset", 0), a.get("limit", 20)
        return {"offset": off, "limit": lim, "total": len(out), "entries": out[off:off + lim],
                "rarityCounts": counts}

    def species_view(self, sid):
        rep = self.rep() or {}
        if sid == self.species["speciesId"]:
            s = json.loads(json.dumps(self.species))
            s["isRepresentative"] = rep.get("speciesId") == sid
            for f in s["forms"]:
                f["isRepresentative"] = s["isRepresentative"] and rep.get("form") == f["form"]
            return s
        c = self.cell(sid)
        active = self.state["companion"].get("active") or {}
        individuals = []
        for e in self.log:
            if e["speciesId"] != sid:
                continue
            individuals.append({
                "id": e["id"], "form": e["form"], "displayName": e["displayName"], "isRaising": e["isRaising"],
                "released": False, "isShiny": e["isShiny"], "caughtAt": e["caughtAt"], "nature": e["nature"],
                "level": active.get("level", 5) if e["isRaising"] else 100, "gender": None, "ability": None,
                "ivs": {"hp": 31, "attack": 20, "defense": 12, "specialAttack": 8, "specialDefense": 25, "speed": 30},
                "stats": None, "moves": [], "prayers": 0})
        return {
            "speciesId": sid, "displayName": c["displayName"] if c else f"#{sid}",
            "rarity": c["rarity"] if c else None, "region": region_of(sid), "owned": c is not None,
            "hasNormal": bool(c and c["hasNormal"]), "isShiny": bool(c and c["isShiny"]),
            "isRaising": bool(c and c["isRaising"]), "isRepresentative": rep.get("speciesId") == sid,
            "forms": [], "individuals": individuals if c else [], "detailsStatus": "unavailable", "details": None}

    def retire_active(self, state, released, now_ts):
        """Replace the raising catch-log entry with a finished one (graduated or sent off)."""
        active = state["companion"]["active"]
        old = next((e for e in self.log if e["isRaising"]), None)
        self.log = [e for e in self.log if not e["isRaising"]]
        reached = [n for n in active["evoLine"] if n["reached"]]
        self.released_n = getattr(self, "released_n", 0) + 1
        entry_id = f"{'released' if released else 'graduated'}-{self.released_n:04d}"
        self.log.append({
            "id": entry_id, "isRaising": False, "released": released, "rarity": active["rarity"],
            "isShiny": active["isShiny"], "nature": active["nature"], "form": active["form"], "caughtAt": now_ts,
            "speciesId": active["speciesId"], "displayName": active["displayName"],
            "chain": [{"speciesId": n["speciesId"], "form": n["form"], "displayName": n["displayName"]}
                      for n in reached]})
        for c in self.cells:
            if c["speciesId"] == active["speciesId"]:
                c["isRaising"] = False
        return old, entry_id

    def set_representative(self, a):
        sid = a.get("speciesId")
        if sid is None:
            self.state["companion"]["representative"] = None
            return {"representative": None}
        c = self.cell(sid)
        if c is None:
            return None
        rep = {"speciesId": sid, "form": a.get("form"), "shiny": bool(c["isShiny"])}
        self.state["companion"]["representative"] = rep
        return {"representative": rep}


def main():
    if len(sys.argv) < 2 or sys.argv[1] != "serve":
        log("usage: ptb-helper serve")
        return 2
    cfg = load_config()
    base = stage_sprites()

    with open(os.path.join(TEST, "fixtures", cfg["fixture"]), encoding="utf-8") as f:
        state = json.load(f)
    now = datetime.now(timezone.utc).replace(microsecond=0)
    delta = now - parse_ts(state["meta"]["generatedAt"])
    state = rebase(state, delta)
    subject = state["companion"]["menuSubject"]
    reply = sprite_reply({"egg": True} if subject["egg"] else {
        "speciesId": subject["speciesId"], "form": subject["form"], "shiny": subject["shiny"],
        "back": subject["back"], "animated": True}, base)
    if isinstance(reply, dict):
        subject["sprite"] = {"key": subject["sprite"]["key"], "manifestPath": reply["manifestPath"],
                             "status": "ready" if reply["manifestPath"].endswith("manifest.json") else "fallback"}
    else:
        subject["sprite"] = {"key": subject["sprite"]["key"], "manifestPath": None, "status": "loading"}

    if "shopDifficulty" in cfg:
        state["settingsApplied"]["shopDifficulty"] = clamp_difficulty(cfg["shopDifficulty"])
    if "wallet" in cfg:
        state["companion"]["wallet"] = int(cfg["wallet"])
    state["companion"]["inventory"].update(cfg.get("inventory", {}))
    if state["companion"]["egg"] is not None:
        state["companion"]["egg"].setdefault("prayers", 0)
        if is_int(cfg.get("eggPrayers")) and cfg["eggPrayers"] >= 0:
            state["companion"]["egg"]["prayers"] = cfg["eggPrayers"]
    derive(state)

    dex = Dex(state, int(cfg.get("dexPad", 0)))
    delayed = []  # [(due monotonic time, message or callable)] for speciesDelay and celebration windows
    rng = random.Random(7)  # useMint

    save_fx = {name: load_fixture(name + ".json")
               for name in ("export-save", "inspect-save", "import-save", "create-snapshot")}
    snapshots = load_fixture("list-snapshots.json")["snapshots"]  # historical: not rebased, so ids and dates agree

    events = []
    with open(os.path.join(TEST, "fixtures", "events.ndjson"), encoding="utf-8") as f:
        events = [json.loads(line) for line in f if line.strip()]

    write({"type": "hello", "protocol": cfg.get("protocol", 1), "helperVersion": "0.0.0-stub", "pid": os.getpid()})
    state["meta"]["fromCache"] = True
    write(state)

    def emit_state(**meta):
        state["meta"].update(meta)
        state["meta"]["generatedAt"] = fmt_ts(datetime.now(timezone.utc))
        write(state)

    def fail(cid, code, message):
        write({"type": "result", "id": cid, "ok": False, "error": {"code": code, "message": message}})

    def ok(cid, data):
        write({"type": "result", "id": cid, "ok": True, "data": data})

    def today_ts():
        """A timestamp on the fixture's calendar day, so new entries land in the recap's current week."""
        return f"{state['usage']['calendar']['today']}T{datetime.now(timezone.utc).strftime('%H:%M:%S')}Z"

    def current_summary():
        return {"dexCount": sum(1 for e in dex.log if not e["isRaising"]),
                "lifetimeTokens": save_fx["inspect-save"]["current"]["lifetimeTokens"]}

    def new_snapshot():
        """A snapshot of the in-memory state, newest first, capped at 10 (save-transfer.md §3)."""
        now_dt = datetime.now(timezone.utc)
        stamp = now_dt.astimezone(LOCAL_TZ).strftime("%Y-%m-%d-%H%M%S")
        taken, sid, n = {s["id"] for s in snapshots}, f"companion-snapshot-{stamp}.json", 0
        while sid in taken:
            n += 1
            sid = f"companion-snapshot-{stamp}-{n}.json"
        a = state["companion"]["active"]
        snap = dict(id=sid, date=fmt_ts(now_dt), speciesId=a["speciesId"] if a else None,
                    shiny=bool(a and a["isShiny"]), form=a["form"] if a else None, **current_summary())
        snapshots.insert(0, snap)
        del snapshots[10:]
        return snap

    def check_path(cid, args):
        """The shared path validation of the file commands; True when the command may go on."""
        path = args.get("path")
        if not isinstance(path, str) or not path.startswith("/") or "\0" in path:
            fail(cid, "badArgs", "path must be an absolute file path")
            return False
        name = os.path.basename(path).lower()
        for needle, code, message in FILE_ERRORS:
            if needle in name:
                fail(cid, code, message)
                return False
        return True

    def start_egg(tier):
        c = state["companion"]
        growth = state["settingsApplied"].get("growthDifficulty", 1.0)
        c.update(phase="egg", mood="egg", active=None)
        c["egg"] = {"used": 0, "threshold": rnd(5_000_000 * clamp_difficulty(growth)), "imminent": False,
                    "hatchDelayed": False, "guarantee": tier, "prayers": 0}
        if c["representative"] is None:
            r = sprite_reply({"egg": True}, base)
            c["menuSubject"] = {"egg": True, "speciesId": None, "form": None, "shiny": False, "back": False,
                                "sprite": {"key": "egg", "manifestPath": r["manifestPath"] if isinstance(r, dict) else None,
                                           "status": "ready" if isinstance(r, dict) else "loading"}}

    def celebrate(kind, name, seconds):
        c = state["companion"]
        before = c["mood"]
        c["levelUpUntil"] = fmt_ts(datetime.now(timezone.utc) + timedelta(seconds=seconds))
        c["mood"] = "levelUp"
        if kind == "evolved":
            c["justEvolvedTo"] = name
        else:
            c["justGraduated"] = name

        def clear():
            c["levelUpUntil"] = c["justEvolvedTo"] = c["justGraduated"] = None
            c["mood"] = "egg" if c["phase"] == "egg" else (before if before != "levelUp" else "idle")
            emit_state()
        delayed.append((time.monotonic() + seconds, clear))

    def apply_candy_xp(xp):
        """bag-shop.md §2.2 applyUsage: evolve with carry-over, graduate and discard the rest."""
        c = state["companion"]
        a = c["active"]
        growth = state["settingsApplied"].get("growthDifficulty", 1.0)
        a["usedAtStage"] += xp
        events, result = [], "progressed"
        while True:
            costs = stage_costs(a, growth)
            if a["usedAtStage"] < costs[0]:
                break
            a["usedAtStage"] -= costs[0]
            if a["stageIndex"] == a["totalForms"] - 1:
                _, entry_id = dex.retire_active(state, False, today_ts())
                events.append({"type": "event", "kind": "graduate", "speciesId": a["speciesId"], "form": a["form"],
                               "displayName": a["displayName"], "rarity": a["rarity"], "isShiny": a["isShiny"],
                               "entryId": entry_id})
                start_egg(None)
                celebrate("graduated", a["displayName"], 6)
                return events, "graduated"
            before = dict(speciesId=a["speciesId"], displayName=a["displayName"])
            a["stageIndex"] += 1
            line = a["evoLine"]
            for i, node in enumerate(line):
                node["current"] = i == a["stageIndex"]
                node["reached"] = i <= a["stageIndex"]
            if a["stageIndex"] < len(line) and line[a["stageIndex"]]["speciesId"] is not None:
                a["speciesId"], a["displayName"] = line[a["stageIndex"]]["speciesId"], line[a["stageIndex"]]["displayName"]
            a["isFinalStage"] = a["stageIndex"] == a["totalForms"] - 1
            a["threshold"] = stage_costs(a, growth)[0]
            if c["representative"] is None and not c["menuSubject"]["egg"]:
                c["menuSubject"]["speciesId"] = a["speciesId"]
            events.append({"type": "event", "kind": "evolve", "fromSpeciesId": before["speciesId"],
                           "fromDisplayName": before["displayName"], "speciesId": a["speciesId"], "form": a["form"],
                           "displayName": a["displayName"], "isShiny": a["isShiny"]})
            celebrate("evolved", a["displayName"], 4)
            result = "evolved"
        a["threshold"] = stage_costs(a, growth)[0]
        return events, result

    start = time.monotonic()
    next_event = start + cfg["eventsAfter"] if cfg["eventsAfter"] >= 0 else None
    event_index = 0
    buf = b""
    fd = sys.stdin.fileno()
    while True:
        due = [t for t in (next_event, min((d for d, _ in delayed), default=None)) if t is not None]
        timeout = None if not due else max(0.0, min(due) - time.monotonic())
        readable, _, _ = select.select([fd], [], [], timeout)
        if not readable:
            now_m = time.monotonic()
            if delayed and min(d for d, _ in delayed) <= now_m:
                for d, m in [x for x in delayed if x[0] <= now_m]:
                    m() if callable(m) else write(m)
                delayed[:] = [x for x in delayed if x[0] > now_m]
                continue
            if next_event is None:
                continue
            if event_index < len(events):
                write(events[event_index])
                event_index += 1
                next_event = time.monotonic() + cfg["eventGap"]
            else:
                next_event = None
            continue
        chunk = os.read(fd, 65536)
        if not chunk:
            log("stdin EOF, exiting")
            return 0
        buf += chunk
        while b"\n" in buf:
            line, buf = buf.split(b"\n", 1)
            try:
                msg = json.loads(line)
            except ValueError:
                log(f"bad line: {line[:200]!r}")
                continue
            cid = msg.get("id")
            if not isinstance(cid, int) or isinstance(cid, bool) or cid <= 0:
                log("command without a positive id ignored")
                continue
            cmd = msg.get("cmd")
            args = msg.get("args") or {}
            if cmd in ("ping", "wake", "networkUp"):
                write({"type": "result", "id": cid, "ok": True, "data": {}})
            elif cmd == "setSettings":
                if "shopDifficulty" in args and (not isinstance(args["shopDifficulty"], (int, float))
                                                 or isinstance(args["shopDifficulty"], bool)):
                    fail(cid, "badArgs", "shopDifficulty must be a number")
                    continue
                applied = dict(state["settingsApplied"])  # optional fields keep their last value (§9.2)
                applied.update(args)
                for key in ("growthDifficulty", "shopDifficulty"):
                    if key in applied:
                        applied[key] = clamp_difficulty(applied[key])
                state["settingsApplied"] = applied
                derive(state)  # prices follow shopDifficulty
                emit_state(fromCache=False)
                ok(cid, applied)
            elif cmd == "refresh":
                emit_state(refreshing=True)
                emit_state(refreshing=False, fromCache=False, lastUpdated=fmt_ts(datetime.now(timezone.utc)))
                write({"type": "result", "id": cid, "ok": True, "data": {}})
            elif cmd == "stubSilent":
                pass  # test hook: never answers, so the extension-side timeout fires
            elif cmd == "stubExit":
                log("test hook: exiting with status 1")
                return 1
            elif cmd == "getSprite":
                r = sprite_reply(args, base)
                if isinstance(r, dict):
                    write({"type": "result", "id": cid, "ok": True, "data": r})
                elif r == "badArgs":
                    write({"type": "result", "id": cid, "ok": False,
                           "error": {"code": "badArgs", "message": "invalid sprite request (speciesId 1..649, or a valid item name)"}})
                else:
                    write({"type": "result", "id": cid, "ok": False,
                           "error": {"code": "unavailable", "message": "no staged sprite"}})
            elif cmd == "getDexPage":
                write({"type": "result", "id": cid, "ok": True, "data": dex.page(args)})
            elif cmd == "getCatchLog":
                write({"type": "result", "id": cid, "ok": True, "data": dex.catch_log(args)})
            elif cmd == "getSpecies":
                sid = args.get("speciesId")
                if not isinstance(sid, int) or not 1 <= sid <= 649:
                    write({"type": "result", "id": cid, "ok": False,
                           "error": {"code": "badArgs", "message": "speciesId must be 1..649"}})
                    continue
                reply = {"type": "result", "id": cid, "ok": True, "data": dex.species_view(sid)}
                if cfg.get("speciesDelay", 0) > 0:
                    delayed.append((time.monotonic() + cfg["speciesDelay"], reply))
                    log(f"getSpecies {sid} delayed {cfg['speciesDelay']} s")
                else:
                    write(reply)
            elif cmd == "setRepresentative":
                data = dex.set_representative(args)
                if data is None:
                    write({"type": "result", "id": cid, "ok": False,
                           "error": {"code": "notOwned", "message": "that species or form is not owned"}})
                    continue
                rep = data["representative"]
                subject = state["companion"]["menuSubject"]
                if rep:
                    subject.update(speciesId=rep["speciesId"], form=rep["form"], shiny=rep["shiny"], egg=False)
                emit_state()
                write({"type": "result", "id": cid, "ok": True, "data": data})
            elif cmd == "buyItem":
                item, count = args.get("item"), args.get("count", 1)
                if item not in ITEMS or not is_int(count) or count < 1:
                    fail(cid, "badArgs", "item must be rareCandy, mint or shinyCharm and count an int >= 1")
                    continue
                entry = next(e for e in state["companion"]["shop"]["entries"] if e["item"] == item)
                if entry["passive"] and count != 1:
                    fail(cid, "badArgs", f"{item} is bought one at a time")
                elif entry["purchased"]:
                    fail(cid, "alreadyOwned", f"{item} is already owned")
                elif count > entry["maxCount"]:
                    fail(cid, "insufficientFunds", "not enough tokens")
                else:
                    c = state["companion"]
                    spent = entry["price"] * count
                    c["wallet"] -= spent
                    c["inventory"][item] += count
                    derive(state)
                    emit_state()
                    ok(cid, {"item": item, "count": count, "spent": spent, "wallet": c["wallet"],
                             "owned": c["inventory"][item]})
            elif cmd == "buyEgg":
                c = state["companion"]
                if "tier" not in args or args["tier"] not in (None, "uncommon", "rare"):
                    fail(cid, "badArgs", "tier must be null, uncommon or rare")
                    continue
                tier = args["tier"]
                entry = next(e for e in c["shop"]["entries"] if e["kind"] == "egg" and e["tier"] == tier)
                if c["active"] is None:
                    fail(cid, "noActive", "an egg is already incubating")
                elif c["wallet"] < entry["price"]:
                    fail(cid, "insufficientFunds", "not enough tokens")
                else:
                    c["wallet"] -= entry["price"]
                    _, entry_id = dex.retire_active(state, True, today_ts())
                    start_egg(tier)
                    derive(state)
                    emit_state()
                    ok(cid, {"tier": tier, "spent": entry["price"], "wallet": c["wallet"],
                             "releasedEntryId": entry_id})
            elif cmd == "planCandies":
                count = args.get("count")
                if not is_int(count):
                    fail(cid, "badArgs", "count must be an int")
                    continue
                c = state["companion"]
                growth = state["settingsApplied"].get("growthDifficulty", 1.0)
                ok(cid, {"plan": make_plan(c["active"], c["bag"]["candyMaxUse"], count, growth)})
            elif cmd == "useCandy":
                count = args.get("count")
                c = state["companion"]
                if not is_int(count) or count < 1:
                    fail(cid, "badArgs", "count must be an int >= 1")
                elif c["active"] is None:
                    fail(cid, "noActive", "needs a hatched Pokémon")
                elif c["inventory"]["rareCandy"] <= 0:
                    fail(cid, "noStock", "no Rare Candy in the bag")
                elif c["bag"]["candyBlocked"] is not None:
                    fail(cid, "notReady", "the evolution line is still loading")
                else:
                    n = min(count, c["bag"]["candyMaxUse"])
                    c["inventory"]["rareCandy"] -= n
                    events, result = apply_candy_xp(n * CANDY_XP)
                    derive(state)
                    emit_state()
                    for ev in events:
                        write(ev)
                    ok(cid, {"consumed": n, "xp": n * CANDY_XP, "result": result})
            elif cmd == "useMint":
                c = state["companion"]
                if c["active"] is None:
                    fail(cid, "noActive", "needs a hatched Pokémon")
                elif c["inventory"]["mint"] <= 0:
                    fail(cid, "noStock", "no Mint in the bag")
                else:
                    pool = [n for n in NATURES if n != c["active"]["nature"]]
                    nature = pool[rng.randrange(len(pool))]
                    c["active"]["nature"] = nature
                    c["inventory"]["mint"] -= 1
                    for e in dex.log:
                        if e["isRaising"]:
                            e["nature"] = nature
                    derive(state)
                    emit_state()
                    ok(cid, {"nature": nature})
            elif cmd == "pray":
                egg = state["companion"]["egg"]
                if egg is None:
                    fail(cid, "notEgg", "a Pokémon is active; prayers count for an egg")
                else:
                    egg["prayers"] += 1
                    emit_state()
                    ok(cid, {"count": egg["prayers"]})
            elif cmd == "exportSave":
                if check_path(cid, args):
                    ok(cid, dict(save_fx["export-save"], path=args["path"], summary=current_summary()))
            elif cmd == "inspectSave":
                if check_path(cid, args):
                    ok(cid, dict(save_fx["inspect-save"], current=current_summary()))
            elif cmd == "importSave":
                if not check_path(cid, args):
                    continue
                digest = args.get("digest")
                if not isinstance(digest, str) or not digest:
                    fail(cid, "badArgs", "digest must be the digest inspectSave returned")
                elif "changed" in os.path.basename(args["path"]).lower() or digest != save_fx["inspect-save"]["digest"]:
                    fail(cid, "fileChanged", "the file changed after it was inspected")
                else:
                    emit_state()
                    ok(cid, save_fx["import-save"])
            elif cmd == "listSnapshots":
                ok(cid, {"snapshots": snapshots})
            elif cmd == "createSnapshot":
                ok(cid, {"snapshot": new_snapshot()})
            elif cmd == "restoreSnapshot":
                sid = args.get("id")
                if not isinstance(sid, str) or not SNAP_ID_RE.match(sid):
                    fail(cid, "badArgs", "id must be a snapshot file name")
                    continue
                target = next((s for s in snapshots if s["id"] == sid), None)
                if target is None:
                    fail(cid, "notFound", "no such snapshot")
                    continue
                target = dict(target)  # read before the safety snapshot can prune it
                new_snapshot()
                emit_state()
                ok(cid, {"summary": {"dexCount": target["dexCount"], "lifetimeTokens": target["lifetimeTokens"]}})
            elif cmd == "getRecap":
                scope, offset = args.get("scope"), args.get("offset")
                if scope not in ("week", "month", "year") or not is_int(offset) or not -1000 <= offset <= 0:
                    fail(cid, "badArgs", "scope must be week, month or year and offset -1000..0")
                    continue
                ok(cid, make_recap(state, dex.log, scope, offset))
            else:
                write({"type": "result", "id": cid, "ok": False,
                       "error": {"code": "unknownCommand", "message": f"unknown command {cmd!r}"}})


if __name__ == "__main__":
    sys.exit(main())
