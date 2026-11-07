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

/* -- the window host --------------------------------------------------------
 * BKHost holds function pointers, so these trampolines turn each call into a
 * wazero import. `data` is the context, which is how Go finds its window. */
SHIM_IMPORT("host_request") void bui_go_host_request(uint32_t ctx);
SHIM_IMPORT("host_set_cursor") void bui_go_host_set_cursor(uint32_t ctx, int32_t cursor);
SHIM_IMPORT("host_warp") void bui_go_host_warp(uint32_t ctx, int32_t x, int32_t y);
SHIM_IMPORT("host_clipboard_get") uint32_t bui_go_host_clipboard_get(uint32_t ctx);
SHIM_IMPORT("host_clipboard_set") void bui_go_host_clipboard_set(uint32_t ctx, uint32_t text);
SHIM_IMPORT("host_schedule") void bui_go_host_schedule(uint32_t ctx, int32_t ms, int32_t timer);

static void shim_request(void *data)
{
  bui_go_host_request((uint32_t)(uintptr_t)data);
}

static void shim_set_cursor(void *data, int cursor)
{
  bui_go_host_set_cursor((uint32_t)(uintptr_t)data, cursor);
}

static void shim_warp(void *data, int x, int y)
{
  bui_go_host_warp((uint32_t)(uintptr_t)data, x, y);
}

/* Go allocates the result with the module's malloc, and the kit frees it. */
static char *shim_clipboard_get(void *data)
{
  return (char *)(uintptr_t)bui_go_host_clipboard_get((uint32_t)(uintptr_t)data);
}

static void shim_clipboard_set(void *data, const char *text)
{
  bui_go_host_clipboard_set((uint32_t)(uintptr_t)data, (uint32_t)(uintptr_t)text);
}

static void shim_schedule(void *data, int ms, int timer)
{
  bui_go_host_schedule((uint32_t)(uintptr_t)data, ms, timer);
}

SHIM_EXPORT("bui_context_set_window_host") void bui_context_set_window_host(BKContext *ctx)
{
  BKHost host = {ctx,
                 shim_request,
                 shim_set_cursor,
                 shim_warp,
                 shim_clipboard_get,
                 shim_clipboard_set,
                 shim_schedule,
                 shim_emit};
  BK_context_set_host(ctx, &host);
}
