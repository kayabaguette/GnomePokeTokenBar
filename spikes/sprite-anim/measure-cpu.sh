#!/usr/bin/env bash
# Average CPU usage of the gnome-shell process over a time window.
#
#   ./measure-cpu.sh [seconds=60] [pid]
#
# Reads utime+stime (all threads) from /proc/<pid>/stat before and after the
# window. The result is a percentage of ONE core (like top). If pidstat is
# installed it samples the same window as a cross-check.
#
# Without a PID it picks this user's only gnome-shell process. With a nested
# or headless shell running there are two: pass the PID explicitly
# (`pgrep -a gnome-shell` lists them).
set -euo pipefail

secs=${1:-60}
pid=${2:-}

if [[ -z $pid ]]; then
    mapfile -t pids < <(pgrep -u "$(id -u)" -x gnome-shell || true)
    if (( ${#pids[@]} != 1 )); then
        echo "found ${#pids[@]} gnome-shell processes (${pids[*]:-none}); pass the PID: $0 $secs <pid>" >&2
        exit 1
    fi
    pid=${pids[0]}
fi
[[ -r /proc/$pid/stat ]] || { echo "no such process: $pid" >&2; exit 1; }

hz=$(getconf CLK_TCK)

# utime + stime in clock ticks. Fields are counted after the ")" that closes
# the command name, which may itself contain spaces.
cpu_ticks() {
    local rest
    rest=$(<"/proc/$pid/stat")
    rest=${rest##*) }
    local -a f
    read -r -a f <<<"$rest"
    echo $(( f[11] + f[12] )) # fields 14 (utime) and 15 (stime)
}

echo "gnome-shell pid $pid: sampling ${secs}s ..."
t0=$(date +%s%N)
c0=$(cpu_ticks)
if command -v pidstat >/dev/null; then
    pidstat -h -u -p "$pid" "$secs" 1 | awk '$1 !~ /^#/ && NF { line = $0 } END { print "pidstat:", line }'
else
    sleep "$secs"
fi
c1=$(cpu_ticks)
t1=$(date +%s%N)

awk -v c="$(( c1 - c0 ))" -v hz="$hz" -v ns="$(( t1 - t0 ))" 'BEGIN {
    sec = ns / 1e9
    printf "cpu %.2f s over %.1f s wall = %.2f %% of one core\n", c / hz, sec, 100 * (c / hz) / sec
}'
