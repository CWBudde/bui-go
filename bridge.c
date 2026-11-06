/* SPDX-License-Identifier: GPL-2.0-or-later */
#include "bridge.h"

#include "_cgo_export.h"

void bui_node_set_handle(BKNode *node, uintptr_t handle)
{
  BK_node_set_ref(node, (void *)handle);
}

uintptr_t bui_node_handle(const BKNode *node)
{
  return (uintptr_t)BK_node_ref(node);
}

/* `ref` is deliberately unused: it is NULL for CLICK and CHANGE, the handler is
 * found through the node. Some kinds (BK_EV_CALL) arrive with a NULL node, and
 * kit-internal nodes have no handle; both are ignored before entering Go. */
static void bui_emit(void *data, BKNode *node, int kind, void *ref, double a, double b)
{
  (void)data;
  (void)ref;
  (void)b;
  if (!node) {
    return;
  }
  const uintptr_t handle = bui_node_handle(node);
  if (handle) {
    buiGoEmit(handle, kind, a);
  }
}

void bui_context_set_emit_host(BKContext *ctx)
{
  BKHost host = {0};
  host.emit = bui_emit;
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
