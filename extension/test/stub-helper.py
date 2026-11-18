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
    getSprite (staged sprites), anything else unknownCommand;
  - emits each line of fixtures/events.ndjson after `eventsAfter` seconds,
    `eventGap` seconds apart (negative eventsAfter disables events);
  - exits 0 on stdin EOF.

Test hooks: `stubSilent` never answers; `stubExit` exits with status 1.
Environment overrides: PTB_STUB_PROTOCOL, PTB_STUB_EVENTS_AFTER.

Config: <ext>/test/stub-config.json, e.g.
  {"fixture": "state-active.json", "eventsAfter": 6, "eventGap": 1, "protocol": 1}
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
        timeout = None if next_event is None else max(0.0, next_event - time.monotonic())
        readable, _, _ = select.select([fd], [], [], timeout)
        if not readable:
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
            else:
                write({"type": "result", "id": cid, "ok": False,
                       "error": {"code": "unknownCommand", "message": f"unknown command {cmd!r}"}})


if __name__ == "__main__":
    sys.exit(main())
