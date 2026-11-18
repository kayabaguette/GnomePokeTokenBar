#!/usr/bin/env bash
# Build a sandbox copy of the extension that talks to the protocol stub.
#
#   extension/test/prepare-sandbox.sh DEST_DIR [fixture] [eventsAfter]
#
# Writes DEST_DIR/poketokenbar@local/ with compiled schemas, bin/ptb-helper
# replaced by test/stub-helper.py, a generated sprite cache under
# test/sprite-fixture/ and test/stub-config.json. Then run, for example:
#
#   scripts/headless-shell.sh --ext DEST_DIR/poketokenbar@local \
#       --enable poketokenbar@local --duration 20 --cycle
#
# Never touches the live session: it only writes under DEST_DIR.
set -euo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
DEST_ROOT=${1:?usage: prepare-sandbox.sh DEST_DIR [fixture] [eventsAfter]}
FIXTURE=${2:-state-active.json}
EVENTS_AFTER=${3:-6}
DEST=$DEST_ROOT/poketokenbar@local

rm -rf "$DEST"
mkdir -p "$DEST"
cp -a "$REPO/extension/." "$DEST/"
glib-compile-schemas --strict "$DEST/schemas"

rm -f "$DEST/bin/ptb-helper"
mkdir -p "$DEST/bin"
cp "$REPO/extension/test/stub-helper.py" "$DEST/bin/ptb-helper"
chmod +x "$DEST/bin/ptb-helper"

gjs -m "$REPO/extension/test/make-sprite-fixture.js" "$REPO/spikes/sprite-anim/out" "$DEST/test/sprite-fixture"

EXTRA=${PTB_STUB_EXTRA:+, $PTB_STUB_EXTRA}
printf '{"fixture": "%s", "eventsAfter": %s, "eventGap": 1, "protocol": 1%s}\n' "$FIXTURE" "$EVENTS_AFTER" "$EXTRA" \
    >"$DEST/test/stub-config.json"
echo "prepared $DEST (fixture $FIXTURE, events after ${EVENTS_AFTER}s)"
