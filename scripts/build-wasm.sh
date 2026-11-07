#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# Builds BUI's C kit at the commit pinned in BUI_COMMIT as a WebAssembly
# reactor module for wazero, with a pinned
# wasi-sdk. Outputs, under the gitignored build/wasm/out:
#
#   bui_kit.wasm     kit + gpu + core + wasm/shim.c, exports in wasm/exports.txt;
#                    also copied to internal/engine/wasmengine, where it is
#                    committed and embedded
#   sjlj_probe.wasm  FreeType alone + wasm/sjlj_probe.c, for the setjmp test;
#                    also copied to internal/engine/wasmengine/testdata, where it
#                    is committed so that the test runs everywhere
#
# BUILD_JOBS limits parallel compiles (default 4).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
commit="$(tr -d '[:space:]' <"$root/BUI_COMMIT")"
src="$root/third_party/BUI"
jobs="${BUILD_JOBS:-4}"

# The SDK release and the SHA-256 of its Linux x86_64 archive (computed on
# download; the release publishes no digest). Its clang, not the system one,
# compiles everything, so builds do not depend on the host.
sdk_version=34.0
sdk_tag=wasi-sdk-34
sdk_sha256=b761e3a0721dbae9c09a0059e5fdb2bf917d1b4a8a7b430fb3b5aafb0984b2c4
sdk_archive="wasi-sdk-$sdk_version-x86_64-linux.tar.gz"
sdk="$root/third_party/wasi-sdk/wasi-sdk-$sdk_version-x86_64-linux"

build="$root/build/wasm"
out="$build/out"

if [ "$(uname -s)-$(uname -m)" != Linux-x86_64 ]; then
	echo "build-wasm: only the Linux x86_64 wasi-sdk is pinned so far" >&2
	exit 1
fi

# -- wasi-sdk ----------------------------------------------------------------
if [ ! -x "$sdk/bin/clang" ]; then
	dl="$root/third_party/dl"
	mkdir -p "$dl"
	if [ ! -f "$dl/$sdk_archive" ]; then
		curl -fsSL -o "$dl/$sdk_archive.part" \
			"https://github.com/WebAssembly/wasi-sdk/releases/download/$sdk_tag/$sdk_archive"
		mv "$dl/$sdk_archive.part" "$dl/$sdk_archive"
	fi
	echo "$sdk_sha256  $dl/$sdk_archive" | sha256sum -c --quiet - || {
		echo "build-wasm: checksum mismatch for $dl/$sdk_archive" >&2
		exit 1
	}
	mkdir -p "$root/third_party/wasi-sdk"
	tar -C "$root/third_party/wasi-sdk" -xzf "$dl/$sdk_archive"
fi
if [ "$(head -n1 "$sdk/VERSION")" != "$sdk_version" ]; then
	echo "build-wasm: $sdk is not wasi-sdk $sdk_version" >&2
	exit 1
fi
cc="$sdk/bin/clang"
cxx="$sdk/bin/clang++"

# -- BUI at BUI_COMMIT, unmodified (same check as build-kit.sh) --------------
if [ ! -d "$src/.git" ]; then
	mkdir -p "$root/third_party"
	git init -q "$src"
	git -C "$src" remote add origin https://github.com/ppmpreetham/BUI.git
fi
# Check before moving HEAD, so a dirty tree is reported and left as it is.
if [ -n "$(git -C "$src" status --porcelain)" ]; then
	echo "build-wasm: $src has local changes, so the module would not match BUI_COMMIT:" >&2
	git -C "$src" status --short >&2
	exit 1
fi
if [ "$(git -C "$src" rev-parse HEAD 2>/dev/null || true)" != "$commit" ]; then
	git -C "$src" fetch -q --depth 1 origin "$commit"
	git -C "$src" checkout -q --detach FETCH_HEAD
fi

# -- flags -------------------------------------------------------------------
# setjmp/longjmp (FreeType) and C++ exceptions (Blender's BLI containers use
# try/throw) both lower to the standard exnref opcodes (try_table, throw_ref),
# the only ones wazero implements. LLVM still defaults to the legacy ones.
eh_flags=(-mllvm -wasm-enable-sjlj -mllvm -wasm-use-legacy-eh=false)
# SIMD (in wazero's CoreFeaturesV2) lets the raster loops vectorize as they do
# natively with SSE; docs/design.md, "What the build changed", has the measured effect.
# wasm/compat.h fills libc gaps without touching the BUI tree.
common_flags=("-ffile-prefix-map=$root=." -msimd128 "${eh_flags[@]}" -include "$root/wasm/compat.h")
# The stack goes first, so an overflow traps at address 0 instead of silently
# overwriting data. Nested emits share it. Measured peak: docs/design.md, "Stack".
stack_size=$((1024 * 1024))
link_flags=(
	-mexec-model=reactor
	-fwasm-exceptions
	"-Wl,--stack-first"
	"-Wl,-z,stack-size=$stack_size"
	"-Wl,--gc-sections"
	"-Wl,--strip-debug"
	-lsetjmp
)

# -- kit, gpu, core -----------------------------------------------------------
mkdir -p "$build"
cmake -S "$src" -B "$build/cmake" -G Ninja \
	"-DCMAKE_TOOLCHAIN_FILE=$sdk/share/cmake/wasi-sdk-p1.cmake" \
	"-DWASI_SDK_PREFIX=$sdk" \
	-DCMAKE_BUILD_TYPE=Release \
	"-DCMAKE_C_FLAGS=${common_flags[*]}" \
	"-DCMAKE_CXX_FLAGS=${common_flags[*]} -fwasm-exceptions" \
	-DBUI_TEXT_GDI=OFF \
	-DBUI_BUILD_TESTS=OFF \
	-DBUI_BUILD_EXAMPLES=OFF \
	-DBUI_BUILD_PYTHON_BINDINGS=OFF \
	-DBUI_BUILD_UI=OFF \
	-DBUI_BUILD_QT_BACKEND=OFF \
	-DBUI_BUILD_IMGUI=OFF \
	-DBUI_GPU=OFF \
	-DBUI_VIDEO=OFF \
	-DBUI_FILEBROWSER=OFF \
	>"$build/cmake.log"
cmake --build "$build/cmake" --target bui_kit bui_gpu bui_core -j "$jobs"

mkdir -p "$out" "$build/obj"
kit_inc=(-I"$src/kit/include" -I"$src/gpu/include")
"$cc" -O2 "${common_flags[@]}" "${kit_inc[@]}" -c "$root/wasm/shim.c" -o "$build/obj/shim.o"
"$cc" -O2 "${common_flags[@]}" -c "$root/wasm/compat.c" -o "$build/obj/compat.o"

exports=()
while read -r sym; do
	case "$sym" in '' | '#'*) continue ;; esac
	exports+=("-Wl,--export=$sym")
done <"$root/wasm/exports.txt"

# wasm/compat.c defines the wrappers, so they are the kit's alone, not the probe's.
"$cxx" -O2 "${link_flags[@]}" "${exports[@]}" \
	-Wl,--wrap=malloc -Wl,--wrap=calloc -Wl,--wrap=realloc \
	"$build/obj/shim.o" "$build/obj/compat.o" \
	"$build/cmake/kit/libbui_kit.a" "$build/cmake/gpu/libbui_gpu.a" "$build/cmake/core/libbui_core.a" \
	-lunwind -o "$out/bui_kit.wasm"

# -- the setjmp probe: FreeType alone ------------------------------------------
ft="$src/gpu/extern/freetype"
ft_flags=(-O2 "${common_flags[@]}" -I"$ft/bui" -I"$ft/include"
	'-DFT_CONFIG_OPTIONS_H=<bui_ftoption.h>' '-DFT_CONFIG_MODULES_H=<bui_ftmodule.h>')
mkdir -p "$build/obj/ft"
probe_objs=()
running=0
# the FreeType sources gpu/CMakeLists.txt compiles into bui_gpu
for s in base/ftsystem.c base/ftinit.c base/ftdebug.c base/ftbase.c base/ftmm.c base/ftbitmap.c \
	base/ftbbox.c truetype/truetype.c sfnt/sfnt.c autofit/autofit.c smooth/smooth.c \
	psnames/psnames.c cff/cff.c psaux/psaux.c pshinter/pshinter.c; do
	o="$build/obj/ft/$(basename "$s" .c).o"
	probe_objs+=("$o")
	"$cc" "${ft_flags[@]}" -DFT2_BUILD_LIBRARY -w -c "$ft/src/$s" -o "$o" &
	running=$((running + 1))
	if [ "$running" -ge "$jobs" ]; then
		wait -n
		running=$((running - 1))
	fi
done
while [ "$running" -gt 0 ]; do
	wait -n
	running=$((running - 1))
done
"$cc" "${ft_flags[@]}" -I"$src/gpu/extern" -c "$root/wasm/sjlj_probe.c" -o "$build/obj/sjlj_probe.o"
"$cc" -O2 "${link_flags[@]}" -Wl,--wrap=__wasm_longjmp \
	"$build/obj/sjlj_probe.o" "${probe_objs[@]}" -o "$out/sjlj_probe.wasm"

cp "$out/bui_kit.wasm" "$root/internal/engine/wasmengine/bui_kit.wasm"
mkdir -p "$root/internal/engine/wasmengine/testdata"
cp "$out/sjlj_probe.wasm" "$root/internal/engine/wasmengine/testdata/sjlj_probe.wasm"

ls -l "$out"/*.wasm
