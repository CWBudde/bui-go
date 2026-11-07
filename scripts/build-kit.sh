#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# Builds BUI's C kit at the commit pinned in BUI_COMMIT and copies the static
# libraries and public headers into this repository. The CMake options mirror
# bindings/rust/blenderui-sys/build.rs upstream.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
commit="$(tr -d '[:space:]' <"$root/BUI_COMMIT")"
src="$root/third_party/BUI"
build="$root/build"
goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
out="$root/lib/${goos}_${goarch}"

if [ ! -d "$src/.git" ]; then
	mkdir -p "$root/third_party"
	git init -q "$src"
	git -C "$src" remote add origin https://github.com/ppmpreetham/BUI.git
fi
# Check before moving HEAD, so a dirty tree is reported and left as it is.
if [ -n "$(git -C "$src" status --porcelain)" ]; then
	echo "build-kit: $src has local changes, so the libraries would not match BUI_COMMIT:" >&2
	git -C "$src" status --short >&2
	exit 1
fi
if [ "$(git -C "$src" rev-parse HEAD 2>/dev/null || true)" != "$commit" ]; then
	git -C "$src" fetch -q --depth 1 origin "$commit"
	git -C "$src" checkout -q --detach FETCH_HEAD
fi

cmake -S "$src" -B "$build" \
	-DCMAKE_BUILD_TYPE=Release \
	"-DCMAKE_C_FLAGS=-ffile-prefix-map=$root=." \
	"-DCMAKE_CXX_FLAGS=-ffile-prefix-map=$root=." \
	-DBUI_TEXT_GDI=OFF \
	-DBUI_BUILD_TESTS=OFF \
	-DBUI_BUILD_EXAMPLES=OFF \
	-DBUI_BUILD_PYTHON_BINDINGS=OFF \
	-DBUI_BUILD_UI=OFF \
	-DBUI_BUILD_QT_BACKEND=OFF \
	-DBUI_BUILD_IMGUI=OFF
cmake --build "$build" --target bui_kit -j "$(nproc)"

# Without the X11 headers CMake only prints a status line and the kit builds with
# kit_window_null.c: it links, then reports no display forever. Where Wayland support is
# built, BK_window_run moves to kit_window_linux.c and the X11 host defines bk_x11_window_run.
if [ "$goos" = linux ] && ! nm --defined-only "$build/kit/libbui_kit.a" 2>/dev/null |
	awk '/\.o:$/ { x11 = ($0 == "kit_window_x11.c.o:") } x11 && $2 == "T" && ($3 == "BK_window_run" || $3 == "bk_x11_window_run") { found = 1 } END { exit !found }'; then
	echo "build-kit: the X11 window host was not built; install the X11 headers (libx11-dev) and rebuild" >&2
	exit 1
fi

mkdir -p "$out"
for lib in kit gpu core; do
	cp "$build/$lib/libbui_$lib.a" "$out/"
done
strip --strip-debug "$out"/*.a

rm -rf "$root/include"
mkdir -p "$root/include"
cp "$src/kit/include/BUI_kit.h" "$src/gpu/include/GPU_raster.h" "$src/gpu/include/GPU_backend_sink.h" "$root/include/"
cp "$src/LICENSE" "$root/LICENSE"
