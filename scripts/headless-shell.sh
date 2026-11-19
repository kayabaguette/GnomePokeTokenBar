#!/usr/bin/env bash
# Run an isolated, headless GNOME Shell for smoke tests (and screenshots via a test extension).
#
# This script is the ONLY sanctioned way for this project to start a gnome-shell instance.
# See tasks/lessons.md (2026-11-18): an improvised headless run shared the live XDG_RUNTIME_DIR
# and detached the live session's document-portal FUSE mount.
#
# Everything a shell can write to is redirected into .sandbox/:
#   XDG_RUNTIME_DIR (wayland socket, private D-Bus activations, portal FUSE mount),
#   XDG_CONFIG_HOME / XDG_DATA_HOME / XDG_CACHE_HOME / XDG_STATE_HOME,
#   GSettings (memory backend), PokeTokenBar helper state (PTB_STATE_DIR).
# HOME is kept so the helper can read ~/.claude (read-only).
#
# Usage:
#   scripts/headless-shell.sh [--ext DIR]... [--rpm FILE] [--enable UUID]...
#                             [--duration SEC] [--cycle]
#
#   --ext DIR      copy an extension directory (must contain metadata.json) into the sandbox
#   --rpm FILE     extract an RPM into .sandbox/rpmroot and prepend its usr/share to XDG_DATA_DIRS
#   --enable UUID  enable this extension once the shell is up (repeatable)
#   --duration S   seconds to keep the shell running after enabling (default 20)
#   --cycle        disable then re-enable every enabled extension halfway through
#   --seed-state F copy companion save F into the helper state dir (__TODAY__ -> today's date)
#   --prefs UUID   open that extension's preferences window once the shell is up
#
# Output: .sandbox/logs/<timestamp>/ with shell.log, extension-info.txt, isolation.txt.
# Exit status is non-zero if isolation checks fail.
set -euo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
SB=$REPO/.sandbox
RUN=$SB/run
STAMP=$(date +%Y%m%d-%H%M%S)
LOGS=$SB/logs/$STAMP
DURATION=20
CYCLE=0
EXTS=()
ENABLE=()
RPM=""
SEED=""
PREFS=""

die() { echo "headless-shell: $*" >&2; exit 2; }

while (($#)); do
    case "$1" in
        --ext) EXTS+=("$2"); shift 2 ;;
        --rpm) RPM=$(realpath -e "$2") || die "no such RPM: $2"; shift 2 ;;
        --enable) ENABLE+=("$2"); shift 2 ;;
        --duration) DURATION=$2; shift 2 ;;
        --cycle) CYCLE=1; shift ;;
        --seed-state) SEED=$(realpath -e "$2") || die "no such seed: $2"; shift 2 ;;
        --prefs) PREFS=$2; shift 2 ;;
        -h|--help) sed -n '2,26p' "$0"; exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done

[[ $DURATION =~ ^[0-9]+$ ]] || die "--duration must be an integer"
# A unix socket path must fit in 108 bytes ("$RUN/wayland-0.lock" is the longest we create).
((${#RUN} + 20 < 108)) || die "sandbox runtime path too long for unix sockets: $RUN"
command -v gnome-shell >/dev/null || die "gnome-shell not found"
command -v dbus-run-session >/dev/null || die "dbus-run-session not found"

# Kill every process whose environment points at our private runtime dir, then unmount and
# delete it. Never follows mounts out of the sandbox (rm --one-file-system).
sandbox_pids() {
    local p
    for p in /proc/[0-9]*; do
        [[ -O $p ]] || continue # only our own processes; others' environ is unreadable anyway
        if { tr '\0' '\n' <"$p/environ"; } 2>/dev/null | grep -qxF "XDG_RUNTIME_DIR=$RUN"; then
            echo "${p#/proc/}"
        fi
    done
}

sandbox_mounts() { findmnt -rn -o TARGET | grep -F "$RUN/" | sort -r || true; }

# Never fails: it must always reach the isolation report.
teardown() {
    local pids m
    pids=$(sandbox_pids | grep -vx "$$" || true)
    if [[ -n $pids ]]; then
        echo "sandbox processes still alive after the shell exited (TERM): $(echo $pids)" >>"$LOGS/isolation.txt"
        kill -TERM $pids 2>/dev/null || true
        sleep 1
        pids=$(sandbox_pids | grep -vx "$$" || true)
        if [[ -n $pids ]]; then kill -KILL $pids 2>/dev/null || true; fi
    fi
    if [[ -d $RUN ]]; then
        for m in $(sandbox_mounts); do
            echo "unmounting $m" >>"$LOGS/isolation.txt"
            fusermount3 -u -z "$m" 2>/dev/null || fusermount -u -z "$m" 2>/dev/null || true
        done
        for _ in $(seq 1 20); do [[ -z $(sandbox_mounts) ]] && break; sleep 0.25; done
        rm -rf --one-file-system "$RUN" 2>>"$LOGS/isolation.txt" ||
            echo "WARN: could not fully remove $RUN" >>"$LOGS/isolation.txt"
    fi
    return 0
}

# Fresh sandbox (logs from previous runs are kept).
mkdir -p "$LOGS"
teardown
rm -rf --one-file-system "$SB/config" "$SB/data" "$SB/cache" "$SB/state" "$SB/ptb-state" "$SB/rpmroot"
mkdir -p "$RUN" "$SB/config" "$SB/data/gnome-shell/extensions" "$SB/cache" "$SB/state" "$SB/ptb-state"
chmod 0700 "$RUN"
trap teardown EXIT

if [[ -n $SEED ]]; then
    sed "s/__TODAY__/$(date +%F)/" "$SEED" >"$SB/ptb-state/companion-state.json"
fi

for d in "${EXTS[@]}"; do
    [[ -f $d/metadata.json ]] || die "no metadata.json in $d"
    uuid=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["uuid"])' "$d/metadata.json")
    cp -a "$d" "$SB/data/gnome-shell/extensions/$uuid"
done

DATA_DIRS=${XDG_DATA_DIRS:-/usr/local/share:/usr/share}
if [[ -n $RPM ]]; then
    mkdir -p "$SB/rpmroot"
    (cd "$SB/rpmroot" && rpm2cpio "$RPM" | cpio -idm --quiet)
    DATA_DIRS=$SB/rpmroot/usr/share:$DATA_DIRS
fi

# Isolation baseline.
LIVE_UID=$(id -u)
LIVE_RUNTIME=/run/user/$LIVE_UID
ls -A "$LIVE_RUNTIME" | sort >"$LOGS/runtime-before.txt"
live_mounts() { findmnt -rn -o TARGET,FSTYPE | grep -E "^$LIVE_RUNTIME(/| )" | sort || true; }
live_mounts >"$LOGS/live-mounts-before.txt"
# Live shells only: a gnome-shell whose XDG_RUNTIME_DIR is the live one. Another agent's sandbox
# shell (private runtime dir) must not make this run's isolation check fail.
live_shells() {
    local p
    for p in $(pgrep -u "$LIVE_UID" -x gnome-shell || true); do
        if { tr '\0' '\n' <"/proc/$p/environ" | grep -qx "XDG_RUNTIME_DIR=$LIVE_RUNTIME"; } 2>/dev/null; then
            echo "$p"
        fi
    done | sort | tr '\n' ' '
}
LIVE_SHELL_BEFORE=$(live_shells)

# shellcheck disable=SC2016  # expanded inside the private session
INNER='
set -u
gnome-shell --headless --wayland --no-x11 --virtual-monitor 1280x800 >"$SB_LOGS/shell.log" 2>&1 &
shell=$!
call() { gdbus call --session --dest org.gnome.Shell --object-path /org/gnome/Shell --method "org.gnome.Shell.Extensions.$1" "$2"; }
up=0
for _ in $(seq 1 120); do
    # Ready only once the Extensions interface answers (introspection succeeds earlier).
    if gdbus call --session --dest org.gnome.Shell --object-path /org/gnome/Shell \
        --method org.gnome.Shell.Extensions.ListExtensions >/dev/null 2>&1; then up=1; break; fi
    sleep 0.5
done
[ "$up" = 1 ] || { echo "shell did not come up" >>"$SB_LOGS/shell.log"; kill -TERM $shell; wait $shell; exit 3; }
sleep 2 # let the shell finish its startup animation before enabling
for u in $SB_ENABLE; do echo "enable $u: $(call EnableExtension "$u")" >>"$SB_LOGS/extension-info.txt"; done
if [ -n "$SB_PREFS" ]; then
    sleep 3
    echo "open prefs $SB_PREFS: $(gdbus call --session --dest org.gnome.Shell --object-path /org/gnome/Shell --method org.gnome.Shell.Extensions.OpenExtensionPrefs "$SB_PREFS" "" "{}" 2>&1)" >>"$SB_LOGS/extension-info.txt"
fi
half=$((SB_DURATION / 2))
sleep "$half"
echo "ptb-helper processes while enabled: $(pgrep -f ptb-helper -a | grep -F "$SB_ROOT" | wc -l)" >>"$SB_LOGS/extension-info.txt"
pgrep -f ptb-helper -a | grep -F "$SB_ROOT" | sed "s/^/  /" >>"$SB_LOGS/extension-info.txt"
if [ "$SB_CYCLE" = 1 ]; then
    for u in $SB_ENABLE; do echo "disable $u: $(call DisableExtension "$u")" >>"$SB_LOGS/extension-info.txt"; done
    sleep 4
    echo "ptb-helper processes while disabled: $(pgrep -f ptb-helper -a | grep -F "$SB_ROOT" | wc -l)" >>"$SB_LOGS/extension-info.txt"
    for u in $SB_ENABLE; do echo "re-enable $u: $(call EnableExtension "$u")" >>"$SB_LOGS/extension-info.txt"; done
fi
sleep $((SB_DURATION - half))
for u in $SB_ENABLE; do echo "info $u: $(call GetExtensionInfo "$u")" >>"$SB_LOGS/extension-info.txt"; done
findmnt -rn -o TARGET,FSTYPE | grep -F "$XDG_RUNTIME_DIR" >"$SB_LOGS/private-mounts.txt" || true
kill -TERM $shell
wait $shell
'

set +e
env -u WAYLAND_DISPLAY -u DISPLAY -u DBUS_SESSION_BUS_ADDRESS -u XDG_SESSION_ID \
    XDG_RUNTIME_DIR="$RUN" \
    XDG_CONFIG_HOME="$SB/config" XDG_DATA_HOME="$SB/data" \
    XDG_CACHE_HOME="$SB/cache" XDG_STATE_HOME="$SB/state" \
    XDG_DATA_DIRS="$DATA_DIRS" \
    PTB_STATE_DIR="$SB/ptb-state" \
    GSETTINGS_BACKEND=memory \
    SB_LOGS="$LOGS" SB_ROOT="$SB" SB_DURATION="$DURATION" SB_CYCLE="$CYCLE" SB_ENABLE="${ENABLE[*]}" SB_PREFS="$PREFS" \
    dbus-run-session -- bash -c "$INNER" >"$LOGS/session.log" 2>&1
status=$?
set -e

teardown
trap - EXIT

# Isolation evidence: the live runtime dir and the live shell must be unchanged.
ls -A "$LIVE_RUNTIME" | sort >"$LOGS/runtime-after.txt"
live_mounts >"$LOGS/live-mounts-after.txt"
LIVE_SHELL_AFTER=$(live_shells)
# Entries that other tools on the machine create or remove on their own (rootless containers).
# A change there is reported but cannot come from the sandbox: sandbox processes run with
# XDG_RUNTIME_DIR=$RUN, and the mount comparison below still covers the real risk.
EXTERNAL_RE='^(crun|containers|libpod|netns|podman|buildah.*)$'
ok=1
{
    echo "inner status: $status"
    echo "live gnome-shell before: $LIVE_SHELL_BEFORE"
    echo "live gnome-shell after:  $LIVE_SHELL_AFTER"
    changed=$(comm -3 "$LOGS/runtime-before.txt" "$LOGS/runtime-after.txt" | tr -d '\t' || true)
    for e in $changed; do
        if [[ $e =~ $EXTERNAL_RE ]]; then
            echo "WARN: live $LIVE_RUNTIME/$e changed (container runtime, not the sandbox)"
        else
            echo "FAIL: live $LIVE_RUNTIME/$e appeared or disappeared"; ok=0
        fi
    done
    if ! diff -u "$LOGS/live-mounts-before.txt" "$LOGS/live-mounts-after.txt"; then
        echo "FAIL: mounts under live $LIVE_RUNTIME changed"; ok=0
    fi
    [[ $LIVE_SHELL_BEFORE == "$LIVE_SHELL_AFTER" ]] || { echo "FAIL: live shell PIDs changed"; ok=0; }
    leftovers=$(sandbox_pids | grep -vx "$$" || true)
    [[ -z $leftovers ]] || { echo "FAIL: leftover sandbox processes: $(echo $leftovers)"; ok=0; }
    [[ -z $(sandbox_mounts) ]] || { echo "FAIL: sandbox mounts remain: $(sandbox_mounts)"; ok=0; }
    echo "private mounts seen during the run (expected inside $RUN):"
    cat "$LOGS/private-mounts.txt" 2>/dev/null || true
    if ((ok)); then echo "ISOLATION OK"; else echo "ISOLATION FAILED"; fi
} >>"$LOGS/isolation.txt" 2>&1

echo "logs: $LOGS"
cat "$LOGS/isolation.txt"
((ok)) || exit 1
exit "$status"
