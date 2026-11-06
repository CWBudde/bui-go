/* SPDX-License-Identifier: GPL-2.0-or-later */
/*
 * libc gaps of wasm32-wasip1 that the kit links against. The BUI tree stays
 * unmodified: a definition here wins over libc.a's member, which the linker
 * then never pulls in.
 */
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#ifdef __wasm_simd128__
#  include <wasm_simd128.h>
#endif

/* wasi-sdk's prebuilt libc is built without __wasilibc_simd_string, so its
 * memcmp compares byte by byte. The raster's damage pass (gpu_raster.c,
 * BUI_raster_end_frame) memcmps every tile row of every frame against the
 * previous one, which made memcmp half of a small render's time. This one
 * compares 16 bytes per step and falls back to bytes to find the first
 * difference, so results equal the byte loop's. */
int memcmp(const void *a, const void *b, size_t n)
{
  const unsigned char *l = (const unsigned char *)a;
  const unsigned char *r = (const unsigned char *)b;
#ifdef __wasm_simd128__
  while (n >= 16) {
    const v128_t eq = wasm_i8x16_eq(wasm_v128_load(l), wasm_v128_load(r));
    if (!wasm_i8x16_all_true(eq)) {
      break; /* the byte loop below finds the first difference in these 16 */
    }
    l += 16;
    r += 16;
    n -= 16;
  }
#endif
  for (; n; n--, l++, r++) {
    if (*l != *r) {
      return *l - *r;
    }
  }
  return 0;
}
