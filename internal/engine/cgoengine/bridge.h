/* SPDX-License-Identifier: GPL-2.0-or-later */
#pragma once

#include <stdint.h>

#include "BUI_kit.h"

/* The node's id travels in the kit's void *ref, so no Go pointer ever crosses
 * into C. 0 means "no id". */
void bui_node_set_id(BKNode *node, uint32_t id);
uint32_t bui_node_id(const BKNode *node);

/* Installs the emit-only host. Must run before BK_window_new, which saves the
 * host it finds and forwards only emit. */
void bui_context_set_emit_host(BKContext *ctx);

/* Installs a host that forwards to Go's window host of ctx as well as emit. */
void bui_context_set_window_host(BKContext *ctx);

/* BK_window_wake_init with the Go wake callback. */
void bui_wake_init(void);
