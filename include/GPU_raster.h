/* BUI software raster backend.
 *
 * A complete frame renderer for hosts without a GPU context (Tk, plain Qt
 * widgets, headless). It plays the role Blender's GPU module + BLF play: the
 * whole UI is drawn into one RGB32 frame buffer in native code, then the host
 * presents it with a single blit — instead of turning every primitive into a
 * host-toolkit object.
 *
 *  - Anti-aliased analytic primitives (rect, rounded box, circle, line) using
 *    signed-distance coverage, like Blender's widget-base shaders.
 *  - Generic triangles with per-vertex color (for the BuiGpuSink seam).
 *  - Native text (GDI glyph-run cache on Windows); hosts fall back to their
 *    own text drawing where BUI_text_native() is 0.
 *  - Damage tracking: after each frame, the tiles that actually changed are
 *    reported so hosts upload only those.
 *
 * Pixel layout: uint32 0x00RRGGBB, top-down rows, stride == width. This is
 * byte-compatible with a Win32 32bpp BI_RGB DIB and QImage::Format_RGB32.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later */
#pragma once

#include <stddef.h>
#include <stdint.h>

#include "GPU_backend_sink.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct BuiRaster BuiRaster;

enum {
  BUI_FONT_BOLD = 1 << 0,
  BUI_FONT_ITALIC = 1 << 1,
  BUI_FONT_UNDERLINE = 1 << 2,
};

BuiRaster *BUI_raster_create(int width, int height);
void BUI_raster_free(BuiRaster *r);

/* Frame bracket. begin_frame resizes (and fully damages) when the size
 * changes; end_frame computes the damage list against the previous frame. */
void BUI_raster_begin_frame(BuiRaster *r, int width, int height);
void BUI_raster_end_frame(BuiRaster *r);

int BUI_raster_width(const BuiRaster *r);
int BUI_raster_height(const BuiRaster *r);
const uint32_t *BUI_raster_pixels(const BuiRaster *r);

/* Damage from the last end_frame as {x, y, w, h} rects; returns the count. */
int BUI_raster_damage(const BuiRaster *r, const int (**r_rects)[4]);
/* Force the next end_frame to report the full frame as damaged. */
void BUI_raster_invalidate(BuiRaster *r);

/* Clip subsequent drawing; w/h <= 0 removes the clip. */
void BUI_raster_set_clip(BuiRaster *r, int x, int y, int w, int h);

void BUI_raster_fill_rect(BuiRaster *r, float x, float y, float w, float h, const float rgba[4]);
void BUI_raster_frame_rect(
    BuiRaster *r, float x, float y, float w, float h, float thickness, const float rgba[4]);
void BUI_raster_round_rect(
    BuiRaster *r, float x, float y, float w, float h, float radius, const float rgba[4]);
/* line_width <= 0 draws a filled disc. */
void BUI_raster_circle(
    BuiRaster *r, float cx, float cy, float radius, float line_width, const float rgba[4]);
void BUI_raster_line(
    BuiRaster *r, float x1, float y1, float x2, float y2, float width, const float rgba[4]);
/* Axis-aligned ellipse; line_width <= 0 fills. */
void BUI_raster_ellipse(BuiRaster *r,
                        float cx,
                        float cy,
                        float rx,
                        float ry,
                        float line_width,
                        const float rgba[4]);
/* Filled polygon (even-odd), anti-aliased; `points` holds `n` x/y pairs. */
void BUI_raster_polygon(BuiRaster *r, const float (*points)[2], int n, const float rgba[4]);

/* Rounded-box corner mask (Blender's UI_CNR_*). */
enum {
  BUI_CORNER_TOP_LEFT = 1 << 0,
  BUI_CORNER_TOP_RIGHT = 1 << 1,
  BUI_CORNER_BOTTOM_RIGHT = 1 << 2,
  BUI_CORNER_BOTTOM_LEFT = 1 << 3,
  BUI_CORNER_ALL = 15,
};

/* One Blender widget base (the GPU_SHADER_2D_WIDGET_BASE look) in a single
 * pass: inner fill shaded from `inner_top` to `inner_bottom`, an
 * `outline_width` outline inside the edge, and an emboss line just below
 * the box. Corners not in `corners` are square. Any color may be NULL. */
void BUI_raster_widget(BuiRaster *r,
                       float x,
                       float y,
                       float w,
                       float h,
                       float radius,
                       int corners,
                       const float inner_top[4],
                       const float inner_bottom[4],
                       const float outline[4],
                       float outline_width,
                       const float emboss[4]);

/* Parameters of Blender's widget-base shader (WidgetBaseParameters), in
 * screen space (y down). */
typedef struct BuiWidgetBase {
  float rect[4];        /* x, y, w, h of the outer shape */
  float rad, radi;      /* outer / inner corner radius */
  int corners;          /* BUI_CORNER_* mask */
  float inner1[4];      /* shade start: top (or left when shade_horizontal) */
  float inner2[4];      /* shade end: bottom (or right) */
  float outline[4];
  float emboss[4];
  int shade_horizontal; /* Blender's shade_dir == 0 */
  int alpha_checker;    /* checkerboard under the inner color */
  float pixelsize;      /* U.pixelsize */
} BuiWidgetBase;

void BUI_raster_widget_base(BuiRaster *r, const BuiWidgetBase *params);

/* An image: premultiplied 0xAARRGGBB (rows top-down, stride w: the layout
 * of the frame itself and of the GPU atlas, so drawing needs no conversion)
 * or a video frame in NV12 (BUI_PIXMAP_NV12_*: `px` points at w * h bytes
 * of luma, then w * h / 2 of interleaved Cb Cr at half resolution; w and h
 * even; limited range; opaque). `key` names the pixels for the GPU
 * renderers: nonzero and unique per content (the kit's BKImBuf keys); 0
 * hashes the pixels at each draw (small, short-lived images). `stream`
 * nonzero marks successive frames of one stream (a video): GPU renderers
 * keep one texture for it and update it in place when `key` changes;
 * BUI_image_forget(stream) frees it. */
typedef struct BuiPixmap {
  const uint32_t *px;
  int w, h;
  uint64_t key;
  int format;      /* BUI_PIXMAP_* */
  uint64_t stream; /* 0: a still image */
} BuiPixmap;
enum {
  BUI_PIXMAP_ARGB = 0,      /* premultiplied 0xAARRGGBB */
  BUI_PIXMAP_NV12_BT601 = 1, /* YCbCr 4:2:0, BT.601 (SD video) */
  BUI_PIXMAP_NV12_BT709 = 2, /* YCbCr 4:2:0, BT.709 (HD video) */
};
enum {
  BUI_IMAGE_NEAREST = 1 << 0, /* nearest texel (CSS image-rendering: pixelated); else bilinear */
};
/* Rows y0 .. y0 + rows of `img` as premultiplied 0xAARRGGBB (`dst`: w per
 * row): a copy, or NV12 converted the way drawing converts it. */
void BUI_pixmap_read(const BuiPixmap *img, int y0, int rows, uint32_t *dst);
/* Draws the `src` part of `img` (x, y, w, h in image pixels; NULL = all of
 * it) into x, y, w, h, filtered, times `alpha`, premultiplied source-over.
 * 1:1 on whole pixels is an exact copy. */
void BUI_raster_image(BuiRaster *r,
                      float x,
                      float y,
                      float w,
                      float h,
                      const BuiPixmap *img,
                      const float src[4],
                      float alpha,
                      int flags);

/* The pixels named `key` (BuiPixmap.key) are gone for good: GPU renderers
 * free their copy (images too big for the atlas keep a texture of their own).
 * Any thread. The kit calls it when it frees a decoded image. */
void BUI_image_forget(uint64_t key);
/* Images up to this many pixels on a side share the GPU atlas; bigger ones
 * get a texture of their own (forget those with BUI_image_forget). */
#define BUI_IMAGE_ATLAS_MAX 2047

/* end_frame that only diffs the given region (everything else is known to
 * be unchanged): partial redraws cost O(region), not O(window). */
void BUI_raster_end_frame_region(BuiRaster *r, int x, int y, int w, int h);

/* Soft drop shadow around a rounded box (menus/popups), fading out over
 * `size` pixels from `alpha`. */
void BUI_raster_shadow(
    BuiRaster *r, float x, float y, float w, float h, float radius, float size, float alpha);
void BUI_raster_triangles(BuiRaster *r,
                          const float (*coords)[2],
                          const float (*colors)[4],
                          int count);
/* HSV color wheel (Blender's layout: red at the bottom, saturation grows
 * outward) at brightness `value`, anti-aliased edge. */
void BUI_raster_hsv_circle(BuiRaster *r, float cx, float cy, float radius, float value);
/* Rectangle with bilinear corner colors (top-left, top-right, bottom-right,
 * bottom-left). */
void BUI_raster_gradient(BuiRaster *r,
                         float x,
                         float y,
                         float w,
                         float h,
                         const float tl[4],
                         const float tr[4],
                         const float br[4],
                         const float bl[4]);
/* Lit sphere (normal ball): `light` is the direction (x right, y up, z out). */
void BUI_raster_sphere(
    BuiRaster *r, float cx, float cy, float radius, const float light[3], const float rgba[4]);
void BUI_raster_points(BuiRaster *r,
                       const float (*coords)[2],
                       const float (*colors)[4],
                       int count,
                       float size);

/* -- textures and textured meshes -------------------------------------------
 * Indexed triangle meshes with per-vertex position, texture coordinates and
 * color, sampling one texture: the geometry immediate-mode UIs emit (Dear
 * ImGui's ImDrawList, see bindings/imgui). Every renderer draws them: the
 * software raster, Direct3D 11 and OpenGL.
 *
 * Textures are RGBA8 (bytes R, G, B, A in memory; straight alpha), owned by
 * the library and shared by every raster, CPU or GPU: a GPU device uploads a
 * texture on first use and only the changed rectangles after an update. Ids
 * are > 0 and fit in an int (or an ImTextureID). Create, update and free them
 * on the thread that draws; drawing only reads them (also from band threads). */
int BUI_texture_create(int w, int h, const void *rgba, int pitch); /* rgba NULL: transparent;
                                                                    * pitch 0: w * 4. 0 on failure */
/* Copies a w x h block into the texture at x, y (clipped). Returns 0 for a bad id. */
int BUI_texture_update(int tex, int x, int y, int w, int h, const void *rgba, int pitch);
void BUI_texture_free(int tex);
int BUI_texture_size(int tex, int *r_w, int *r_h); /* 0 when `tex` is not a live texture */

/* One mesh vertex: the memory layout of Dear ImGui's ImDrawVert. `col` is
 * 0xAABBGGRR (bytes R, G, B, A in memory: IM_COL32), straight alpha. */
typedef struct BuiMeshVertex {
  float x, y; /* position in pixels (before the mesh offset) */
  float u, v; /* texture coordinates, 0..1 over the texture */
  uint32_t col;
} BuiMeshVertex;

enum {
  BUI_MESH_INDEX32 = 1 << 0, /* `idx` holds uint32_t indices (else uint16_t) */
  BUI_MESH_NEAREST = 1 << 1, /* nearest-texel sampling (else bilinear) */
};

/* Draws `n_idx / 3` triangles: vertex `vtx[idx[i]]` translated by (ox, oy),
 * colored `col * texel(u, v)` and blended source-over with straight alpha
 * (Dear ImGui's SRC_ALPHA, ONE_MINUS_SRC_ALPHA). `tex` 0 samples white.
 * Pixels whose centers fall inside a triangle are drawn (top-left rule,
 * 1/256 px vertex precision, like D3D): anti-aliasing comes from the mesh
 * itself (ImGui's AA fringes). `clip` (x0, y0, x1, y1 in vertex space, also
 * translated; NULL = none) narrows the raster's clip, truncated to whole
 * pixels like a scissor rectangle. Works in raster views (band threads). */
void BUI_raster_mesh(BuiRaster *r,
                     const BuiMeshVertex *vtx,
                     int n_vtx,
                     const void *idx,
                     int n_idx,
                     int flags,
                     int tex,
                     const float clip[4],
                     float ox,
                     float oy);

/* Text. `family` NULL/"" means the platform UI font; `size` is the em size in
 * pixels; y is the baseline. Returns 0 when no native text engine exists. */
int BUI_text_native(void);
void BUI_raster_set_font(BuiRaster *r, const char *family, int flags);
int BUI_raster_text(BuiRaster *r,
                    float x,
                    float y,
                    const char *utf8,
                    int len,
                    float size,
                    const float rgba[4]);
int BUI_text_measure(
    const char *family, int flags, float size, const char *utf8, int len, float *r_width);

/* Parallel rasterisation. A view draws into `src`'s pixels, clipped to
 * src's clip and the rectangle [x0, x1) x [y0, y1); views over disjoint
 * rectangles may be drawn into from different threads. `dst` points at
 * BUI_raster_view_size() bytes. Views only draw: no frames, damage or present. */
size_t BUI_raster_view_size(void);
void BUI_raster_view(const BuiRaster *src, BuiRaster *dst, int x0, int y0, int x1, int y1);

/* Text in two steps so the drawing can run on several threads: prepare
 * (one thread; builds the glyph mask) and draw the prepared run (any thread,
 * read-only). Prepared runs stay valid while BUI_text_generation() is
 * unchanged. Prepare returns NULL when there is nothing to draw or no native
 * text engine. */
typedef struct BuiTextRun BuiTextRun;
const BuiTextRun *BUI_text_prepare(
    const char *family, int flags, float size, const char *utf8, int len, float *r_width);
unsigned BUI_text_generation(void);
void BUI_raster_text_run(BuiRaster *r, float x, float y, const BuiTextRun *run, const float rgba[4]);
/* A prepared run's 8-bit coverage mask (row-major, `*r_w` x `*r_h`): the
 * pen origin is at (`*r_ox`, `*r_ascent`) inside it. For GPU renderers. */
const uint8_t *BUI_text_run_mask(const BuiTextRun *run, int *r_w, int *r_h, int *r_ox, int *r_ascent);
/* Registers a TrueType/OpenType font (copied) under `family` with style
 * `flags` (BUI_FONT_BOLD | BUI_FONT_ITALIC). Needed where the platform has no
 * font folders (wasm); elsewhere it adds to them. Returns 0 on bad data. */
int BUI_text_add_font(const char *family, int flags, const void *data, int len);
/* The embedded default UI face (Inter, Blender's UI font: the variable
 * TrueType, read-only, static). NULL when the build left it out. */
const void *BUI_text_default_font(int *r_len);

/* GPU rendering (Direct3D 11 on Windows, OpenGL 3.3 on Linux). A raster in
 * GPU mode records its primitives as instances and draws them on the GPU at
 * present; it keeps the same API, clip and damage semantics. attach() creates
 * the device on a worker thread (frames keep using the software raster
 * meanwhile) unless `wait`; poll() switches over once it is ready and returns
 * 1 when the renderer changed, so the caller must redraw the whole frame. A
 * NULL window renders offscreen (BUI_raster_pixels reads the frame back).
 * `window_handle` is an HWND on Windows; on X11 it points to a
 * BuiGpuX11Window (copied during the call). */
typedef struct BuiGpuX11Window {
  void *display;        /* Display * the window lives on (NULL: open one) */
  unsigned long window; /* Window */
} BuiGpuX11Window;
int BUI_raster_gpu_attach(BuiRaster *r, void *window_handle, int wait);
int BUI_raster_gpu_poll(BuiRaster *r);
int BUI_raster_is_gpu(const BuiRaster *r);
/* Whether new windows use the GPU: BUI_gpu_set_default(), overridden by the
 * BUI_GPU environment variable (1/0). Off by default. */
void BUI_gpu_set_default(int on);
int BUI_gpu_default(void);
/* How the GPU renderer draws text: 1 = Slug (glyph outlines evaluated in the
 * pixel shader; sharp at any size), 0 = per-glyph bitmap atlas (Blender's
 * approach; less GPU work). Takes effect on the next frame. Default: the
 * BUI_SLUG build option (ON), overridden by the BUI_SLUG variable (1/0). */
void BUI_gpu_set_slug(int on);
int BUI_gpu_slug(void);

/* A BuiGpuSink that renders into `r` (user pointer is the raster). */
const BuiGpuSink *BUI_raster_sink(BuiRaster *r);

/* Blit damaged regions (or everything when `full`) straight to a native
 * window: an HWND on Windows. Returns 0 when unsupported on this platform. */
int BUI_raster_present_native(BuiRaster *r, void *window_handle, int full);

#ifdef __cplusplus
}
#endif
