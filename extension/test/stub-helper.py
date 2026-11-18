#!/usr/bin/python3 -I
"""Protocol stub standing in for bin/ptb-helper in sandbox smoke tests.

Installed as <ext>/bin/ptb-helper by extension/test/prepare-sandbox.sh. One
process, no threads, no children: stdin is polled with select() so the
headless script's helper count stays exact and EOF ends the process.

Behaviour (docs/protocol.md):
  - prints hello, then the fixture state (fromCache: true) with every
    timestamp rebased to now and the menu-subject sprite pointed at the
    staged sprite cache;
  - answers every command id: ping/wake/networkUp {}, setSettings (echo,
    state first), refresh (refreshing state, fresh state, result),
    getSprite (staged sprites), getDexPage / getCatchLog / getSpecies /
    setRepresentative (from fixtures/dex-page.json, catch-log.json and
    species.json, filtered, sorted and paged here), anything else
    unknownCommand;
  - emits each line of fixtures/events.ndjson after `eventsAfter` seconds,
    `eventGap` seconds apart (negative eventsAfter disables events);
  - exits 0 on stdin EOF.

Test hooks: `stubSilent` never answers; `stubExit` exits with status 1.
Environment overrides: PTB_STUB_PROTOCOL, PTB_STUB_EVENTS_AFTER.

Config: <ext>/test/stub-config.json, e.g.
  {"fixture": "state-active.json", "eventsAfter": 6, "eventGap": 1, "protocol": 1,
   "dexPad": 30, "speciesDelay": 0}
  dexPad adds that many synthetic owned species (and one graduation each) so
  the grid and the log have several pages; speciesDelay delays getSpecies
  replies by that many seconds (to keep a command pending across a disable).

The dex answers only mimic the helper (helper/internal/companion/views.go):
the fixtures, not this file, are the contract.
"""

import json
import os
import re
import select
import shutil
import sys
import time
from datetime import datetime, timedelta, timezone

EXT = os.path.dirname(os.path.dirname(os.path.realpath(__file__)))
TEST = os.path.join(EXT, "test")
TS_RE = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")
STAGED = ["stub-25", "stub-6", "stub-94"]


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
                "stats": None, "moves": []})
        return {
            "speciesId": sid, "displayName": c["displayName"] if c else f"#{sid}",
            "rarity": c["rarity"] if c else None, "region": region_of(sid), "owned": c is not None,
            "hasNormal": bool(c and c["hasNormal"]), "isShiny": bool(c and c["isShiny"]),
            "isRaising": bool(c and c["isRaising"]), "isRepresentative": rep.get("speciesId") == sid,
            "forms": [], "individuals": individuals if c else [], "detailsStatus": "unavailable", "details": None}

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
    state = rebase(state, now - parse_ts(state["meta"]["generatedAt"]))
    subject = state["companion"]["menuSubject"]
    reply = sprite_reply({"egg": True} if subject["egg"] else {
        "speciesId": subject["speciesId"], "form": subject["form"], "shiny": subject["shiny"],
        "back": subject["back"], "animated": True}, base)
    if isinstance(reply, dict):
        subject["sprite"] = {"key": subject["sprite"]["key"], "manifestPath": reply["manifestPath"],
                             "status": "ready" if reply["manifestPath"].endswith("manifest.json") else "fallback"}
    else:
        subject["sprite"] = {"key": subject["sprite"]["key"], "manifestPath": None, "status": "loading"}

    dex = Dex(state, int(cfg.get("dexPad", 0)))
    delayed = []  # [(due monotonic time, message)] for speciesDelay

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
                    write(m)
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
                state["settingsApplied"] = args
                emit_state(fromCache=False)
                write({"type": "result", "id": cid, "ok": True, "data": args})
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
                           "error": {"code": "badArgs", "message": "speciesId must be 1..649"}})
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
            else:
                write({"type": "result", "id": cid, "ok": False,
                       "error": {"code": "unknownCommand", "message": f"unknown command {cmd!r}"}})


if __name__ == "__main__":
    sys.exit(main())
