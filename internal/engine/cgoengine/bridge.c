/* SPDX-License-Identifier: GPL-2.0-or-later */

//go:build cgo && linux && amd64

#include "bridge.h"

#include "_cgo_export.h"

void bui_node_set_id(BKNode *node, uint32_t id)
{
  BK_node_set_ref(node, (void *)(uintptr_t)id);
}

uint32_t bui_node_id(const BKNode *node)
{
  return (uint32_t)(uintptr_t)BK_node_ref(node);
}

/* `ref` is deliberately unused: it is NULL for CLICK and CHANGE, the handler is
 * found through the node. Some kinds (BK_EV_CALL) arrive with a NULL node, and
 * kit-internal nodes have no id; both are ignored before entering Go. */
static void bui_emit(void *data, BKNode *node, int kind, void *ref, double a, double b)
{
  (void)data;
  (void)ref;
  (void)b;
  if (!node) {
    return;
  }
  const uint32_t id = bui_node_id(node);
  if (id) {
    buiGoEmit(id, kind, a);
  }
}

void bui_context_set_emit_host(BKContext *ctx)
{
  BKHost host = {0};
  host.emit = bui_emit;
  BK_context_set_host(ctx, &host);
}

/* `data` is the context, which is how Go finds its window host. */
static void bui_request(void *data)
{
  buiGoHostRequest(data);
}

static void bui_set_cursor(void *data, int cursor)
{
  buiGoHostSetCursor(data, cursor);
}

static void bui_warp(void *data, int x, int y)
{
  buiGoHostWarp(data, x, y);
}

/* C.CString is malloc, and the kit frees the result. */
static char *bui_clipboard_get(void *data)
{
  return buiGoHostClipboardGet(data);
}

static void bui_clipboard_set(void *data, const char *text)
{
  buiGoHostClipboardSet(data, (char *)text);
}

static void bui_schedule(void *data, int ms, int timer)
{
  buiGoHostSchedule(data, ms, timer);
}

void bui_context_set_window_host(BKContext *ctx)
{
  BKHost host = {ctx,
                 bui_request,
                 bui_set_cursor,
                 bui_warp,
                 bui_clipboard_get,
                 bui_clipboard_set,
                 bui_schedule,
                 bui_emit};
  BK_context_set_host(ctx, &host);
}

static void bui_wake(void)
{
  buiGoWake();
}

void bui_wake_init(void)
{
  BK_window_wake_init(bui_wake);
}
