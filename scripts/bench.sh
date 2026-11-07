#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# Records the engines' benchmarks for tracking over time: run it on a quiet machine
# and commit the new file. Compare two with benchstat from golang.org/x/perf.
#
#   scripts/bench.sh [count]   writes bench/<date>-<commit>.txt
#
# Native times move with the machine's load by up to 3x (PLAN.md, Phase 0 results), so
# only ratios from the same run mean much. The load goes in the file for that reason.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
count="${1:-5}"
out="$root/bench/$(date +%F)-$(git -C "$root" rev-parse --short HEAD).txt"

mkdir -p "$root/bench"
cd "$root"
{
	printf '# %s, load: %s\n' "$(git rev-parse HEAD)" "$(cut -d' ' -f1-3 /proc/loadavg)"
	go test -run '^$' -bench . -benchmem -benchtime 200x -count "$count" ./internal/enginetest/
	if command -v xvfb-run >/dev/null; then
		xvfb-run -a go test -run '^$' -bench . -benchmem -benchtime 100x -count "$count" ./internal/host/x11/
	fi
} | tee "$out"
