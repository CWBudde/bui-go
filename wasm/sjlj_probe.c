/* SPDX-License-Identifier: GPL-2.0-or-later */
/*
 * FreeType-only probe for setjmp/longjmp in WebAssembly.
 *
 * FreeType's smooth rasterizer (ftgrays.c) keeps its cells in a fixed pool on
 * the stack. When a glyph needs more cells than the pool holds, it longjmps out
 * of the outline decomposer, halves the band and tries again. A large, complex
 * glyph therefore takes that path every time. This probe renders such a glyph,
 * counts the longjmps and hashes the bitmap, so a test can check that the jump
 * happened and that the result equals a native build of the same sources.
 *
 * Built by scripts/build-wasm.sh into sjlj_probe.wasm, and natively by the
 * spike tests for the reference hash.
 */
#include <stddef.h>
#include <stdint.h>

#include <ft2build.h>
#include FT_FREETYPE_H

#include "font_inter.h"

#ifdef __wasm__
#  define PROBE_EXPORT(name) __attribute__((export_name(name)))
#else
#  define PROBE_EXPORT(name)
#endif

static FT_Library probe_lib;
static FT_Face probe_face;
static uint32_t probe_last_hash;
static int probe_jumps;

/* Every longjmp goes through here (-Wl,--wrap). With -mllvm -wasm-enable-sjlj
 * LLVM rewrites longjmp calls into __wasm_longjmp, natively they stay longjmp. */
#ifdef __wasm__
void __real___wasm_longjmp(void *env, int val);
void __wrap___wasm_longjmp(void *env, int val)
{
  probe_jumps++;
  __real___wasm_longjmp(env, val);
}
#else
#  include <setjmp.h>
_Noreturn void __real_longjmp(jmp_buf env, int val);
_Noreturn void __wrap_longjmp(jmp_buf env, int val)
{
  probe_jumps++;
  __real_longjmp(env, val);
}
#endif

PROBE_EXPORT("probe_init") int probe_init(void)
{
  if (probe_face) {
    return 0;
  }
  FT_Error err = FT_Init_FreeType(&probe_lib);
  if (err) {
    return err;
  }
  return FT_New_Memory_Face(
      probe_lib, g_font_inter_variable, sizeof(g_font_inter_variable), 0, &probe_face);
}

/* Renders one glyph at pixel_size and returns the FreeType error. */
PROBE_EXPORT("probe_render") int probe_render(uint32_t charcode, uint32_t pixel_size)
{
  FT_Error err = probe_init();
  if (err) {
    return err;
  }
  err = FT_Set_Pixel_Sizes(probe_face, 0, pixel_size);
  if (err) {
    return err;
  }
  err = FT_Load_Char(probe_face, charcode, FT_LOAD_RENDER);
  if (err) {
    return err;
  }
  const FT_Bitmap *bm = &probe_face->glyph->bitmap;
  uint32_t h = 2166136261u; /* FNV-1a */
#define PROBE_MIX(v) \
  do { \
    h ^= (uint32_t)(v); \
    h *= 16777619u; \
  } while (0)
  PROBE_MIX(bm->width);
  PROBE_MIX(bm->rows);
  for (unsigned int y = 0; y < bm->rows; y++) {
    const unsigned char *row = bm->buffer + (ptrdiff_t)y * bm->pitch;
    for (unsigned int x = 0; x < bm->width; x++) {
      PROBE_MIX(row[x]);
    }
  }
#undef PROBE_MIX
  probe_last_hash = h;
  return 0;
}

PROBE_EXPORT("probe_hash") uint32_t probe_hash(void)
{
  return probe_last_hash;
}

PROBE_EXPORT("probe_jumps") int probe_jumps_count(void)
{
  return probe_jumps;
}
