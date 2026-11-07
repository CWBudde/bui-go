#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# Requires the committed module to be what the sources build, and within its budget.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module="$root/internal/engine/wasmengine/bui_kit.wasm"
budget=$((8 * 1024 * 1024)) # docs/design.md, "Feasibility criteria"

"$root/scripts/build-wasm.sh"

if ! git -C "$root" diff --exit-code --stat -- internal/engine/wasmengine; then
	echo "check-wasm: the committed modules are not what the sources build" >&2
	exit 1
fi

size="$(wc -c <"$module")"
if [ "$size" -gt "$budget" ]; then
	echo "check-wasm: bui_kit.wasm is $size bytes, over the budget of $budget" >&2
	exit 1
fi
echo "check-wasm: identical, and $size of $budget bytes"
