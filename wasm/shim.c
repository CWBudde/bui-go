/* SPDX-License-Identifier: GPL-2.0-or-later */
/*
 * The wasm counterpart of bridge.c: what the Go side of bui_kit.wasm needs
 * beyond the kit's own exports (listed in wasm/exports.txt).
 *
 * A node's identity is a uint32 id in the kit's `ref` field, since `void *` is
 * 32 bits here and cannot hold a cgo.Handle. Only scalars cross into Go, so Go
 * never parses a C struct layout.
 */
#include <stdint.h>

#include "BUI_kit.h"

#define SHIM_EXPORT(name) __attribute__((export_name(name)))
#define SHIM_IMPORT(name) __attribute__((import_module("bui"), import_name(name)))

/* Go receives every kit event for a node with an id. */
SHIM_IMPORT("emit") void bui_go_emit(uint32_t id, int32_t kind, double a);

SHIM_EXPORT("bui_node_set_id") void bui_node_set_id(BKNode *node, uint32_t id)
{
  BK_node_set_ref(node, (void *)(uintptr_t)id);
}

SHIM_EXPORT("bui_node_id") uint32_t bui_node_id(const BKNode *node)
{
  return (uint32_t)(uintptr_t)BK_node_ref(node);
}

/* As bridge.c: `ref` is NULL for CLICK and CHANGE, the handler is found
 * through the node. Events with a NULL node (BK_EV_CALL) and kit-internal
 * nodes without an id are dropped before they reach Go. */
static void shim_emit(void *data, BKNode *node, int kind, void *ref, double a, double b)
{
  (void)data;
  (void)ref;
  (void)b;
  if (!node) {
    return;
  }
  const uint32_t id = bui_node_id(node);
  if (id) {
    bui_go_emit(id, kind, a);
  }
}

/* Installs the emit-only host. Must run before anything that saves the host. */
SHIM_EXPORT("bui_context_set_emit_host") void bui_context_set_emit_host(BKContext *ctx)
{
  BKHost host = {0};
  host.emit = shim_emit;
  BK_context_set_host(ctx, &host);
}

/* Images queued or decoding (BKImageStats.pending), so Go need not know the
 * struct's layout. */
SHIM_EXPORT("bui_image_pending") int32_t bui_image_pending(void)
{
  BKImageStats st;
  BK_image_stats(&st);
  return st.pending;
}

/* The width from a source's header, read synchronously on this thread, or -1:
 * shows whether a file is reachable at all, independent of the loader. */
SHIM_EXPORT("bui_image_info_width") int32_t bui_image_info_width(const char *src)
{
  BKImageInfo info;
  return BK_image_info(src, &info) ? info.width : -1;
}

/* -- stack diagnostics -------------------------------------------------------
 * With --stack-first the stack is [__stack_low, __stack_high) at the bottom of
 * memory and grows down. bui_stack_paint fills the unused part with a pattern;
 * bui_stack_peak reports how many bytes were ever used since then. */
extern unsigned char __stack_low;
extern unsigned char __stack_high;

#define SHIM_STACK_PATTERN 0x5AFEC0DEu

SHIM_EXPORT("bui_stack_size") uint32_t bui_stack_size(void)
{
  return (uint32_t)(&__stack_high - &__stack_low);
}

SHIM_EXPORT("bui_stack_paint") void bui_stack_paint(void)
{
  volatile uint32_t here = 0;
  /* stay 256 bytes below this frame, which is live */
  uint32_t *end = (uint32_t *)(((uintptr_t)&here - 256u) & ~(uintptr_t)3u);
  for (uint32_t *p = (uint32_t *)(void *)&__stack_low; p < end; p++) {
    *p = SHIM_STACK_PATTERN;
  }
}

SHIM_EXPORT("bui_stack_peak") uint32_t bui_stack_peak(void)
{
  const uint32_t *p = (const uint32_t *)(const void *)&__stack_low;
  const uint32_t *high = (const uint32_t *)(const void *)&__stack_high;
  while (p < high && *p == SHIM_STACK_PATTERN) {
    p++;
  }
  return (uint32_t)((const unsigned char *)high - (const unsigned char *)p);
}
