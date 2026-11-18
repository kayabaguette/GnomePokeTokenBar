#!/usr/bin/env python3
"""Independent oracle for the Claude Code usage totals of the Go helper.

Written from docs/spec/usage.md section 2 only (record format, prefilter, number rules,
id, session id, keep-max dedupe). It shares no code with the Go helper, so a bug has to be
made twice to go unnoticed.

Run it isolated, standard library only:

    python3 -I scripts/oracle_usage.py [--root DIR ...] [--week-start monday]

Output is JSON with aggregate numbers only: no paths, no message content.

Rules implemented (spec section 2):
- files: recursive under each root, hidden names skipped, `.jsonl` only, mtime >= scan start;
- lines split on 0x0A; a trailing line without a newline is still parsed;
- a line containing the bytes "cost-state" (with quotes) is never an assistant line;
- prefilter: line non-empty and contains the bytes "usage" and "assistant" (with quotes);
- record: object, type == "assistant", message is an object, message.usage is an object,
  timestamp is an ISO-8601 string (fraction truncated or padded to 3 digits; Z or +-HH:MM);
- numbers: JSON numbers only, negative -> 0, fraction truncated, >= cap -> cap;
- id = message.id + "|" + requestId (missing or non-string parts are "");
- session = file stem, or the parent of `subagents/`;
- dedupe per file, then across all files: keep the larger total (tie: keep the first seen),
  the earliest date (and its local day), and the union of sessions.
"""

import argparse
import datetime as dt
import json
import os
import re
import sys

# Swift `LocalUsageReader.maxParsedTokenValue` is 1_000_000_000_000_000 (10**15). The spec text
# says 1e18; the constant in the source is what the app does.
CAP = 10**15

COST_STATE = b'"cost-state"'
USAGE = b'"usage"'
ASSISTANT = b'"assistant"'

TS_RE = re.compile(
    r"^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d*))?(Z|[+-]\d{2}:\d{2})$")


def parse_ts(s):
    """ISO-8601 -> integer milliseconds since the epoch, or None."""
    m = TS_RE.match(s)
    if not m:
        return None
    y, mo, d, h, mi, sec, frac, tz = m.groups()
    if frac is not None and not frac.isdigit() and frac != "":
        return None
    ms = int(((frac or "") + "000")[:3])
    try:
        base = dt.datetime(int(y), int(mo), int(d), int(h), int(mi), int(sec), tzinfo=dt.timezone.utc)
    except ValueError:
        return None
    off = 0
    if tz != "Z":
        sign = 1 if tz[0] == "+" else -1
        oh, om = int(tz[1:3]), int(tz[4:6])
        if oh > 23 or om > 59:
            return None
        off = sign * (oh * 60 + om)
    epoch_s = int(base.timestamp()) - off * 60
    return epoch_s * 1000 + ms


def num(v):
    # bool is a subclass of int in Python; the spec counts JSON numbers only.
    if isinstance(v, bool) or not isinstance(v, (int, float)):
        return 0
    if isinstance(v, float) and (v != v or v in (float("inf"), float("-inf"))):
        return 0
    if v <= 0:
        return 0
    if v >= CAP:
        return CAP
    return int(v)


def reject_constant(name):
    raise ValueError(name)


def local_day(ms):
    return dt.datetime.fromtimestamp(ms / 1000).strftime("%Y-%m-%d")


def session_of(path):
    parent = os.path.dirname(path)
    if os.path.basename(parent) == "subagents":
        return os.path.basename(os.path.dirname(parent))
    stem = os.path.basename(path)
    return stem[: -len(".jsonl")] if stem.endswith(".jsonl") else stem


def parse_record(line):
    try:
        text = line.decode("utf-8")
        obj = json.loads(text, parse_constant=reject_constant)
    except (UnicodeDecodeError, ValueError):
        return None
    if not isinstance(obj, dict) or obj.get("type") != "assistant":
        return None
    msg = obj.get("message")
    if not isinstance(msg, dict):
        return None
    usage = msg.get("usage")
    if not isinstance(usage, dict):
        return None
    ts = obj.get("timestamp")
    if not isinstance(ts, str):
        return None
    ms = parse_ts(ts)
    if ms is None:
        return None
    mid = msg.get("id") if isinstance(msg.get("id"), str) else ""
    rid = obj.get("requestId") if isinstance(obj.get("requestId"), str) else ""
    model = msg.get("model") if isinstance(msg.get("model"), str) else "unknown"
    return {
        "id": mid + "|" + rid,
        "ms": ms,
        "model": model,
        "i": num(usage.get("input_tokens")),
        "o": num(usage.get("output_tokens")),
        "cw": num(usage.get("cache_creation_input_tokens")),
        "cr": num(usage.get("cache_read_input_tokens")),
    }


def total(e):
    return e["i"] + e["o"] + e["cw"] + e["cr"]


def dedupe(entries, into=None):
    out = {} if into is None else into
    for e in entries:
        cur = out.get(e["id"])
        if cur is None:
            out[e["id"]] = e
            continue
        kept = dict(e if total(e) > total(cur) else cur)
        kept["ms"] = min(e["ms"], cur["ms"])
        kept["sessions"] = cur["sessions"] | e["sessions"]
        out[e["id"]] = kept
    return out


def parse_file(path, stats):
    with open(path, "rb") as f:
        data = f.read()
    session = session_of(path)
    entries = []
    for line in data.split(b"\n"):
        if COST_STATE in line:
            # Count what the cost-state rule hides, so its size can be reported.
            if USAGE in line and ASSISTANT in line:
                rec = parse_record(line)
                if rec is not None and total(rec) > 0:
                    stats["costStateDroppedRecords"] += 1
                    stats["costStateDroppedTokens"] += total(rec)
            continue
        if not line or USAGE not in line or ASSISTANT not in line:
            continue
        rec = parse_record(line)
        if rec is None:
            continue
        rec["sessions"] = frozenset([session])
        entries.append(rec)
    return list(dedupe(entries).values())


def files_since(roots, since_s):
    out = []
    for root in roots:
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = sorted(d for d in dirnames if not d.startswith("."))
            for name in sorted(filenames):
                if name.startswith(".") or not name.endswith(".jsonl"):
                    continue
                p = os.path.join(dirpath, name)
                try:
                    st = os.stat(p)
                except OSError:
                    continue
                if st.st_mtime_ns >= since_s * 1_000_000_000:
                    out.append(p)
    return out


def scan(roots, since_s, stats):
    merged = {}
    for p in files_since(roots, since_s):
        try:
            entries = parse_file(p, stats)
        except OSError:
            continue
        stats["files"] += 1
        dedupe(entries, merged)
    return list(merged.values())


def bucket(entries, pred):
    b = {"tokens": 0, "input": 0, "output": 0, "cacheWrite": 0, "cacheRead": 0, "records": 0}
    models = {}
    for e in entries:
        if not pred(e):
            continue
        b["input"] += e["i"]
        b["output"] += e["o"]
        b["cacheWrite"] += e["cw"]
        b["cacheRead"] += e["cr"]
        b["tokens"] += total(e)
        b["records"] += 1
        models[e["model"]] = models.get(e["model"], 0) + total(e)
    b["models"] = dict(sorted(models.items()))
    return b


def default_roots():
    home = os.path.expanduser("~")
    roots = []
    for part in os.environ.get("CLAUDE_CONFIG_DIR", "").split(","):
        part = part.strip()
        if part:
            roots.append(os.path.join(os.path.expanduser(part), "projects"))
    xdg = os.environ.get("XDG_CONFIG_HOME", "")
    if xdg and os.path.isabs(xdg):
        roots.append(os.path.join(xdg, "claude", "projects"))
    roots.append(os.path.join(home, ".config", "claude", "projects"))
    roots.append(os.path.join(home, ".claude", "projects"))
    real = []
    for r in roots:
        r = os.path.realpath(r)
        if r not in real:
            real.append(r)
    # Drop nested roots (shortest first), keep order.
    kept = []
    for r in sorted(real, key=len):
        if not any(r == k or r.startswith(k + "/") for k in kept):
            kept.append(r)
    return [r for r in real if r in kept]


WEEKDAYS = ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", action="append", help="projects root (repeatable)")
    ap.add_argument("--week-start", default="monday", choices=WEEKDAYS)
    args = ap.parse_args()
    roots = args.root or default_roots()

    now = dt.datetime.now().astimezone()
    today = now.date()
    midnight = dt.datetime.combine(today, dt.time(0)).astimezone()
    today_key = today.isoformat()

    # Daily scan: files modified since local midnight.
    st_daily = {"files": 0, "costStateDroppedRecords": 0, "costStateDroppedTokens": 0}
    daily = scan(roots, int(midnight.timestamp()), st_daily)
    for e in daily:
        e["day"] = local_day(e["ms"])

    # Enrichment window: min(month start, week start, now-5h, now-7d, first day of 21).
    def day_start(d):
        return dt.datetime.combine(d, dt.time(0)).astimezone()

    month_start = today.replace(day=1)
    back = (today.weekday() - WEEKDAYS.index(args.week_start)) % 7
    week_start = today - dt.timedelta(days=back)
    roll_start = today - dt.timedelta(days=20)
    bound = min(day_start(month_start), day_start(week_start), day_start(roll_start),
                now - dt.timedelta(hours=5), now - dt.timedelta(days=7))
    st_enr = {"files": 0, "costStateDroppedRecords": 0, "costStateDroppedTokens": 0}
    wide = scan(roots, int(bound.timestamp()), st_enr)
    for e in wide:
        e["day"] = local_day(e["ms"])

    series = []
    for i in range(21):
        k = (roll_start + dt.timedelta(days=i)).isoformat()
        series.append(sum(total(e) for e in wide if e["day"] == k))

    ws_key, ms_key = week_start.isoformat(), month_start.isoformat()
    out = {
        "today": today_key,
        "dailyScan": {
            "files": st_daily["files"],
            "costStateDroppedRecords": st_daily["costStateDroppedRecords"],
            "costStateDroppedTokens": st_daily["costStateDroppedTokens"],
            "today": bucket(daily, lambda e: e["day"] == today_key),
        },
        "enrichmentScan": {
            "files": st_enr["files"],
            "costStateDroppedRecords": st_enr["costStateDroppedRecords"],
            "costStateDroppedTokens": st_enr["costStateDroppedTokens"],
            "todayFromWideScan": bucket(wide, lambda e: e["day"] == today_key)["tokens"],
            "week": bucket(wide, lambda e: ws_key <= e["day"] <= today_key)["tokens"],
            "month": bucket(wide, lambda e: ms_key <= e["day"] <= today_key)["tokens"],
            "recentDailyStart": roll_start.isoformat(),
            "recentDaily": series,
        },
    }
    json.dump(out, sys.stdout, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
