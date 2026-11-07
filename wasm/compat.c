/* SPDX-License-Identifier: GPL-2.0-or-later */
/*
 * libc gaps of wasm32-wasip1 that the kit links against. The BUI tree stays
 * unmodified: a definition here wins over libc.a's member, which the linker
 * then never pulls in.
 */
#include <stddef.h>
#include <stdint.h>
#include <string.h>
#include <unistd.h>

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

/* Past the module's memory limit malloc returns NULL, and the kit, like most C code,
 * does not check. Natively that crashes on the first access, but here address 0 is
 * ordinary memory, the bottom of the stack with --stack-first, so a NULL write would
 * corrupt state silently. So running out of memory traps, which package bui's engine
 * reports as fatal. The linker's --wrap sends every call here but the one Go makes,
 * whose NULL it checks itself, so that it can say how much it asked for. */
void *__real_malloc(size_t size);
void *__real_calloc(size_t count, size_t size);
void *__real_realloc(void *p, size_t size);

__attribute__((export_name("malloc"))) void *bui_export_malloc(size_t size)
{
  return __real_malloc(size);
}

__attribute__((noreturn)) static void out_of_memory(void)
{
  static const char msg[] = "bui: the wasm kit ran out of memory\n";
  (void)!write(2, msg, sizeof(msg) - 1);
  __builtin_trap();
}

void *__wrap_malloc(size_t size)
{
  void *p = __real_malloc(size);
  if (!p && size) {
    out_of_memory();
  }
  return p;
}

void *__wrap_calloc(size_t count, size_t size)
{
  void *p = __real_calloc(count, size);
  if (!p && count && size) {
    out_of_memory();
  }
  return p;
}

void *__wrap_realloc(void *old, size_t size)
{
  void *p = __real_realloc(old, size);
  if (!p && size) {
    out_of_memory();
  }
  return p;
}
