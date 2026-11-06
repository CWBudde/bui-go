/* BUI GPU seam: render-backend dispatch.
 *
 * The null backend (gpu/intern/gpu_null.c) turns the toolkit's immediate-mode
 * calls and vertex batches into flat screen-space primitive lists and hands
 * them to a "sink" installed by the host. This is the single integration point
 * that makes BUI renderable with any technology: a C++ QPainter backend
 * (backends/qt), a Python/PySide6 backend (bindings/python/bui/qt.py), an SVG
 * recorder, a terminal grid, etc.
 *
 * Coordinates: 2D positions in the current model space (identity matrices give
 * pixel coordinates). Colors: RGBA floats 0..1, premultiplication not applied.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later */
#pragma once

#ifdef __cplusplus
extern "C" {
#endif

typedef struct BuiGpuSink {
  /** Opaque handle passed back to every callback. */
  void *user;

  /** Optional: called once before a batch of primitives. */
  void (*begin_frame)(void *user, int viewport_w, int viewport_h);
  /** Optional: clip rect; width/height <= 0 removes the clip. */
  void (*scissor)(void *user, int x, int y, int w, int h);
  /** Non-indexed triangles: `count` vertices. */
  void (*triangles)(void *user,
                    const float (*coords)[2],
                    const float (*colors)[4],
                    int count);
  /** Line list: `count` = number of line endpoints. */
  void (*lines)(void *user, const float (*coords)[2], const float (*colors)[4], int count);
  /** Points: `count` vertices at `size` pixels. */
  void (*points)(
      void *user, const float (*coords)[2], const float (*colors)[4], int count, float size);
  /** Optional: text at baseline position. The stub text engine emits nothing
   * unless a host registers glyph runs via BUI_sink_text(). */
  void (*text)(void *user, float x, float y, const char *text, float size, const float rgba[4]);
  /** Optional: called once after a batch of primitives. */
  void (*end_frame)(void *user);
} BuiGpuSink;

/* Install a sink (replacing any previous one). Pass NULL to restore the pure
 * null backend, which discards all geometry. */
void GPU_backend_sink_set(const BuiGpuSink *sink);
const BuiGpuSink *GPU_backend_sink_get(void);

/* Record the active viewport (used by hosts; the sink receives it in
 * begin_frame). Width/height of 0 means "unknown". */
void GPU_viewport_size_set(int width, int height);

#ifdef __cplusplus
}
#endif
