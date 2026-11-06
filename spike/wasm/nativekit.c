//go:build cgo && linux && amd64

/* SPDX-License-Identifier: GPL-2.0-or-later */
#include "nativekit.h"

#include "_cgo_export.h"

void spike_native_set_id(BKNode *node, uint32_t id)
{
  BK_node_set_ref(node, (void *)(uintptr_t)id);
}

uint32_t spike_native_id(const BKNode *node)
{
  return (uint32_t)(uintptr_t)BK_node_ref(node);
}

static void spike_native_emit(void *data, BKNode *node, int kind, void *ref, double a, double b)
{
  (void)data;
  (void)ref;
  (void)b;
  if (!node) {
    return;
  }
  const uint32_t id = spike_native_id(node);
  if (id) {
    spikeNativeEmit(id, kind, a);
  }
}

void spike_native_set_emit_host(BKContext *ctx)
{
  BKHost host = {0};
  host.emit = spike_native_emit;
  BK_context_set_host(ctx, &host);
}

int spike_native_image_pending(void)
{
  BKImageStats st;
  BK_image_stats(&st);
  return st.pending;
}
