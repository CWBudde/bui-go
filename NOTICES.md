# Third-party notices

A program using bui-go contains code from these projects, besides BUI itself. Which ones depends on its engine (see the
[README](README.md#engines)). This file lists them for that purpose. It is not legal advice, and it has had no legal review.

Every licence text linked below is a copy in [`licenses/`](licenses), taken at the pinned version named here unless the section
says otherwise.

## With the wasm engine

| Part                                                                             | Where it ends up            | Licence                                                | Text                                                                                                                                                                                                     |
| -------------------------------------------------------------------------------- | --------------------------- | ------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [wazero](https://github.com/tetratelabs/wazero) v1.12.0                          | the program, as a Go module | Apache-2.0                                             | [wazero.txt](licenses/wazero.txt), [NOTICE](licenses/wazero-NOTICE.txt)                                                                                                                                  |
| [xgb](https://github.com/jezek/xgb) v1.3.1 (Linux)                               | the program, as a Go module | BSD-3-Clause                                           | [xgb.txt](licenses/xgb.txt)                                                                                                                                                                              |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) v0.44.0                  | the program, as a Go module | BSD-3-Clause                                           | [x-sys.txt](licenses/x-sys.txt)                                                                                                                                                                          |
| X11's keysym tables, in `internal/host/x11/keysyms_gen.go`                       | the program (Linux)         | MIT-style (X11)                                        | [x11-keysyms.txt](licenses/x11-keysyms.txt)                                                                                                                                                              |
| [wasi-libc](https://github.com/WebAssembly/wasi-libc), from wasi-sdk 34.0        | `bui_kit.wasm`              | Apache-2.0 WITH LLVM-exception, Apache-2.0 or MIT      | [overview](licenses/wasi-libc.txt), [Apache with LLVM exception](licenses/wasi-libc-LICENSE-APACHE-LLVM.txt), [Apache](licenses/wasi-libc-LICENSE-APACHE.txt), [MIT](licenses/wasi-libc-LICENSE-MIT.txt) |
| musl, in wasi-libc                                                               | `bui_kit.wasm`              | MIT                                                    | [wasi-libc-musl.txt](licenses/wasi-libc-musl.txt)                                                                                                                                                        |
| cloudlibc, in wasi-libc                                                          | `bui_kit.wasm`              | BSD-2-Clause                                           | [wasi-libc-cloudlibc.txt](licenses/wasi-libc-cloudlibc.txt)                                                                                                                                              |
| dlmalloc, in wasi-libc                                                           | `bui_kit.wasm`              | CC0, by the notice in `dlmalloc/src/malloc.c`          | [wasi-libc-dlmalloc.txt](licenses/wasi-libc-dlmalloc.txt), lines 1-5 of that file                                                                                                                        |
| [libc++](https://github.com/llvm/llvm-project), from wasi-sdk 34.0               | `bui_kit.wasm`              | Apache-2.0 WITH LLVM-exception, and older NCSA and MIT | [llvm-libcxx.txt](licenses/llvm-libcxx.txt)                                                                                                                                                              |
| [libc++abi](https://github.com/llvm/llvm-project), from wasi-sdk 34.0            | `bui_kit.wasm`              | Apache-2.0 WITH LLVM-exception, and older NCSA and MIT | [llvm-libcxxabi.txt](licenses/llvm-libcxxabi.txt)                                                                                                                                                        |
| [libunwind](https://github.com/llvm/llvm-project), from wasi-sdk 34.0            | `bui_kit.wasm`              | Apache-2.0 WITH LLVM-exception, and older NCSA and MIT | [llvm-libunwind.txt](licenses/llvm-libunwind.txt)                                                                                                                                                        |
| [compiler-rt](https://github.com/llvm/llvm-project) builtins, from wasi-sdk 34.0 | `bui_kit.wasm`              | Apache-2.0 WITH LLVM-exception, and older NCSA and MIT | [llvm-compiler-rt.txt](licenses/llvm-compiler-rt.txt)                                                                                                                                                    |

wasi-sdk's archive carries none of these licence texts, so they come from the sources it was built from. The tag `wasi-sdk-34`
pins them as submodules, and the archive's own `VERSION` file names the same commits:

- wasi-libc: [`2e6fb9d8ee0cdf9e431fbcabe8af3115de000a13`](https://github.com/WebAssembly/wasi-libc/tree/2e6fb9d8ee0cdf9e431fbcabe8af3115de000a13)
- llvm-project: [`895aa2c896ada719451be2e3673c83da8ddf1141`](https://github.com/llvm/llvm-project/tree/895aa2c896ada719451be2e3673c83da8ddf1141)

wasi-libc also holds `fts` and `emmalloc`, under their own licences. Neither is in `bui_kit.wasm`, whose name section shows
`dlmalloc` and no `fts_open`.

## With either engine

BUI itself is GPL-3.0-or-later as a whole, the text in [LICENSE](LICENSE), and its files keep the SPDX header
`GPL-2.0-or-later`, as its README's "License and credits" states at the pinned commit.

BUI's kit includes these, in `lib/` and in `bui_kit.wasm`. BUI is pinned by the commit in [`BUI_COMMIT`](BUI_COMMIT), and the
texts come from BUI's tree at that commit.

| Part                                                                               | Licence                                  | Text                                                                                                          |
| ---------------------------------------------------------------------------------- | ---------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| [Inter](https://rsms.me/inter), the embedded font                                  | SIL OFL 1.1                              | [Inter.txt](licenses/Inter.txt)                                                                               |
| Blender's sources, through BUI                                                     | GPL-2.0-or-later                         | [LICENSE](LICENSE), and see "Notices inside BUI's sources"                                                    |
| [FreeType](https://freetype.org) 2.13.3                                            | FreeType License or GPL-2.0-or-later     | [freetype.txt](licenses/freetype.txt), [FTL](licenses/freetype-FTL.txt)                                       |
| [stb_truetype](https://github.com/nothings/stb) v1.26                              | MIT or public domain (Unlicense)         | [stb_truetype.txt](licenses/stb_truetype.txt), from the end of `gpu/extern/stb_truetype.h`                    |
| [NanoSVG](https://github.com/memononen/nanosvg), as edited by Blender              | Zlib                                     | [nanosvg.txt](licenses/nanosvg.txt), [nanosvgrast.txt](licenses/nanosvgrast.txt), from the two files' headers |
| [libjpeg-turbo](https://libjpeg-turbo.org) 3.1.2, decoder only                     | IJG, BSD-3-Clause and Zlib               | [libjpeg-turbo.txt](licenses/libjpeg-turbo.txt), [README.ijg](licenses/libjpeg-turbo-README.ijg.txt)          |
| [libwebp](https://chromium.googlesource.com/webm/libwebp) 1.6.0, decoder and demux | BSD-3-Clause, with Google's patent grant | [libwebp.txt](licenses/libwebp.txt), [PATENTS](licenses/libwebp-PATENTS.txt)                                  |
| [Wuffs](https://github.com/google/wuffs), `release/c/wuffs-v0.4.c`                 | Apache-2.0 or MIT                        | [wuffs.txt](licenses/wuffs.txt)                                                                               |

FreeType's `LICENSE.TXT` asks you to choose one of its two licences. bui-go records no choice here. The GPL text it refers to as
`docs/GPLv2.TXT` is not in BUI's tree, and `freetype-FTL.txt` asks for a credit in a product's documentation if you take the
FreeType License.

## With the native engine only

| Part                                                                       | Where it ends up               | Licence                                      | Text                                                                                                                                            |
| -------------------------------------------------------------------------- | ------------------------------ | -------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| GPU glyph coverage ported from [Slug](https://github.com/EricLengyel/Slug) | `libbui_gpu.a`, as shader text | MIT, by the comment in `gpu/intern/gpu_gl.c` | [slug-LICENSE-MIT.txt](licenses/slug-LICENSE-MIT.txt), [slug-NOTICE.txt](licenses/slug-NOTICE.txt), [dual-licence statement](licenses/slug.txt) |

BUI's tree holds only a credit in a comment for this, naming no upstream revision. These texts come from Slug's repository at
[`be3c13eb7d63f9e8aa5c583e42d92c374cb91d98`](https://github.com/EricLengyel/Slug/tree/be3c13eb7d63f9e8aa5c583e42d92c374cb91d98),
its newest commit when they were copied, which need not be the revision BUI ported from. The module is built with `BUI_GPU=OFF`
and does not contain this shader.

A native program also links the C and C++ runtime of the machine it is built on (`-lstdc++ -ldl -lm -lpthread`), and the kit
opens libX11 with `dlopen` at run time. None of these are in this repository.

## Notices inside BUI's sources

These carry their own notice inside files that are otherwise Blender's, with the SPDX header GPL-2.0-or-later unless noted. The
blocks are copied into [bui-source-notices.txt](licenses/bui-source-notices.txt).

| Part                                                     | File                                    | Notice                                              | In `lib/` | In `bui_kit.wasm` |
| -------------------------------------------------------- | --------------------------------------- | --------------------------------------------------- | --------- | ----------------- |
| Atomic operations, from jemalloc                         | `core/intern/atomic/`                   | BSD-2-Clause wording, and GPL for Blender's changes | yes       | yes               |
| List merge sort, by Raja R Harinath                      | `core/intern/list_sort_impl.h`          | MIT wording, no copyright line                      | yes       | no symbol found   |
| Dual quaternion conversion, Trinity College Dublin       | `core/intern/math_rotation_c.c`         | SPDX Zlib, the identifier only                      | yes       | no symbol found   |
| MurmurHash3 and MurmurHash2, by Austin Appleby           | `core/intern/hash_mm3.c`, `hash_mm2.c`  | public domain                                       | yes       | no symbol found   |
| `wcwidth`, by Markus Kuhn                                | `core/intern/wcwidth/wcwidth.c`         | permissive notice                                   | yes       | yes               |
| Platform detection, based on Chromium's `build_config.h` | `core/include/BLI_build_config.h`       | "a BSD-style license", text not in BUI's tree       | headers   | headers           |
| FreeType's hash table                                    | `gpu/extern/freetype/src/base/fthash.c` | X11-style, named in FreeType's `LICENSE.TXT`        | yes       | yes               |

## Credited by BUI, not in these binaries

BUI's [credits](https://github.com/ppmpreetham/BUI/blob/09a154c64cf3b0689517e420aa229e92f0fec11e/website/src/content/docs/project/license-and-credits.mdx)
also list these. bui-go's builds leave them out, as they belong to parts of BUI's tree that are not compiled here:

- Dear ImGui (`BUI_BUILD_IMGUI=OFF` in both build scripts)
- Tailwind CSS's theme values (`bindings/rust`)
- taffy, napi-rs, React and QuickJS (the Rust and JavaScript bindings)
