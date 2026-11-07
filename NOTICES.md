# Third-party notices

A program using bui-go contains code from these projects, besides BUI itself. Which ones depends on its engine (see the
[README](README.md#engines)). This file lists them for that purpose. It is not legal advice.

## With the wasm engine

| Part                                                                      | Where it ends up            | Licence                                                | Text                                                                          |
| ------------------------------------------------------------------------- | --------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------- |
| [wazero](https://github.com/tetratelabs/wazero) v1.12.0                   | the program, as a Go module | Apache-2.0                                             | [wazero.txt](licenses/wazero.txt), [NOTICE](licenses/wazero-NOTICE.txt)       |
| [xgb](https://github.com/jezek/xgb) v1.3.1 (Linux)                        | the program, as a Go module | BSD-3-Clause                                           | [xgb.txt](licenses/xgb.txt)                                                   |
| X11's keysym tables, in `internal/host/x11/keysyms_gen.go`                | the program (Linux)         | MIT-style (X11)                                        | [x11-keysyms.txt](licenses/x11-keysyms.txt)                                   |
| [wasi-libc](https://github.com/WebAssembly/wasi-libc), from wasi-sdk 34.0 | `bui_kit.wasm`              | several, mostly MIT and Apache-2.0 WITH LLVM-exception | [upstream](https://github.com/WebAssembly/wasi-libc/blob/main/LICENSE)        |
| [libc++, libc++abi, libunwind](https://llvm.org), from wasi-sdk 34.0      | `bui_kit.wasm`              | Apache-2.0 WITH LLVM-exception                         | [upstream](https://github.com/llvm/llvm-project/blob/main/libcxx/LICENSE.TXT) |

## With either engine

BUI's kit includes these, in `lib/` and in `bui_kit.wasm`. Their licences are in BUI's tree at the commit in
[`BUI_COMMIT`](BUI_COMMIT).

| Part                                              | Licence                        | Text                                        |
| ------------------------------------------------- | ------------------------------ | ------------------------------------------- |
| [Inter](https://rsms.me/inter), the embedded font | SIL OFL 1.1                    | [Inter.txt](licenses/Inter.txt)             |
| Blender's sources, through BUI                    | GPL-2.0-or-later               | [LICENSE](LICENSE)                          |
| FreeType                                          | FreeType License               | `gpu/extern/freetype/LICENSE.TXT`           |
| libjpeg-turbo                                     | IJG, Modified BSD and zlib     | `kit/extern/libjpeg-turbo/LICENSE.md`       |
| Wuffs                                             | Apache-2.0 or MIT              | `kit/extern/wuffs/LICENSE`                  |
| libwebp, stb_truetype, NanoSVG and more           | as BUI's own credits list them | BUI's `website/.../license-and-credits.mdx` |
