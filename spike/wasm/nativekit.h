/* SPDX-License-Identifier: GPL-2.0-or-later */
#pragma once

#include <stdint.h>

#include "BUI_kit.h"
#include "GPU_raster.h"

/* The spike's emit-only host for the native kit: the node id travels in the
 * kit's ref field as in wasm/shim.c, so both kits share one Go dispatcher. */
void spike_native_set_id(BKNode *node, uint32_t id);
uint32_t spike_native_id(const BKNode *node);
void spike_native_set_emit_host(BKContext *ctx);
int spike_native_image_pending(void);
