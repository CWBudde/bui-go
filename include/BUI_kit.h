/* BUI kit: Blender's interface as a retained-mode C runtime.
 *
 * A tree of nodes (layouts, buttons, fields, menus, views, editors), one
 * input context per window, and a renderer that draws Blender's widgets —
 * ported from editors/interface/interface_widgets.cc — into a BuiRaster.
 *
 * Rendering is damage driven: every state change marks only the node's
 * rectangle dirty, and a frame redraws and diffs just those regions, so an
 * idle UI costs nothing and a hover or a scrub costs microseconds.
 *
 * Hosts (Python, Qt, a native window) only forward input, present the
 * raster and receive callbacks through BKHost.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later */
#pragma once

#include "GPU_raster.h"

/* ---- Version and stability ------------------------------------------------
 *
 * The C API is versioned with BUI itself, as MAJOR.MINOR.PATCH: this header,
 * the Python wheels, the Rust crates and the npm packages all carry the same
 * number (tools/check_versions.py fails a release when they differ).
 *
 * Before 1.0 there is no promise that the API stays the same: any release may
 * add to it, and a release may change or remove a function. Every change that
 * breaks existing code is listed under "C API changes" in CHANGELOG.md, with
 * what to use instead. Pin a release, not a commit.
 *
 * From 1.0 on, semantic versioning: a MAJOR release may break the API, a MINOR
 * release only adds to it, a PATCH release only fixes bugs, and a deprecated
 * function (BUI_DEPRECATED) stays for at least one MINOR release.
 *
 * ABI: nodes, contexts and the other handles are opaque and stay that way. The
 * structs you fill in or read (BKHost, BKMenuSpec, BKImageInfo, BKImageStats,
 * BKCanvasItem, BKCustomEvent) can gain fields before 1.0, so rebuild against
 * the header of the library you link. BK_version() reports the version of the
 * library at run time; compare it with BUI_KIT_VERSION from this header when
 * the two can differ (a shared build, a binding). */
#define BUI_KIT_VERSION_MAJOR 0
#define BUI_KIT_VERSION_MINOR 4
#define BUI_KIT_VERSION_PATCH 1
#define BUI_KIT_VERSION_STRING "0.4.1"
/* One number to compare: 0.4.0 is 400, 1.2.3 is 10203. */
#define BUI_KIT_VERSION \
  (BUI_KIT_VERSION_MAJOR * 10000 + BUI_KIT_VERSION_MINOR * 100 + BUI_KIT_VERSION_PATCH)
#define BUI_KIT_VERSION_AT_LEAST(major, minor, patch) \
  (BUI_KIT_VERSION >= (major) * 10000 + (minor) * 100 + (patch))

/* Marks a function that will be removed; the compiler warns where it is used. */
#if defined(__GNUC__) || defined(__clang__)
#  define BUI_DEPRECATED(message) __attribute__((deprecated(message)))
#elif defined(_MSC_VER)
#  define BUI_DEPRECATED(message) __declspec(deprecated(message))
#else
#  define BUI_DEPRECATED(message)
#endif

#ifdef __cplusplus
extern "C" {
#endif

/* The version of the library you are linked against: BUI_KIT_VERSION as it was
 * when the library was built, and the same as "MAJOR.MINOR.PATCH". */
int BK_version(void);
const char *BK_version_string(void);

typedef struct BKNode BKNode;
typedef struct BKContext BKContext;
typedef struct BKTreeItem BKTreeItem;

/* Modifier / button state bits for input calls. */
enum { BK_SHIFT = 1, BK_CTRL = 2, BK_ALT = 4, BK_BUTTON1 = 8, BK_BUTTON2 = 16, BK_BUTTON3 = 32 };

/* Mouse cursors requested through BKHost.set_cursor. */
enum {
  BK_CURSOR_DEFAULT = 0,
  BK_CURSOR_EW,        /* Blender's horizontal arrows over number fields */
  BK_CURSOR_NS,
  BK_CURSOR_TEXT,
  BK_CURSOR_CROSSHAIR, /* eyedropper, color areas */
  BK_CURSOR_HAND,
  BK_CURSOR_MOVE,      /* panel grip */
  BK_CURSOR_NONE,      /* hidden: continuous grab while scrubbing */
  BK_CURSOR_NESW,      /* diagonal resize (bottom-left / top-right corner) */
  BK_CURSOR_NWSE,      /* diagonal resize (top-left / bottom-right corner) */
  BK_CURSOR_NOT_ALLOWED,
  BK_CURSOR_WAIT,
  BK_CURSOR_PROGRESS,  /* busy in the background, still interactive */
};

/* Callback kinds delivered through BKHost.emit. */
enum {
  BK_EV_CHANGE = 1, /* the node's value changed */
  BK_EV_CLICK,      /* button pressed */
  BK_EV_CALL,       /* menu item / hotkey: call `ref` */
  BK_EV_RENAME,     /* list/tree rename: a = row/item index, text via BK_get_str("edit") */
  BK_EV_ACTIVATE,   /* grid view double-click / Return: a = index */
  BK_EV_TOGGLE,     /* tree toggle column: a = column, b = new value, item = ref */
  BK_EV_BROWSE,     /* path field browse button */
  /* Raw events, for nodes with the "events" property (Tk bind, Qt event
   * handlers): a, b = position relative to the node. */
  BK_EV_PRESS,
  BK_EV_RELEASE,
  BK_EV_DOUBLE,
  BK_EV_MOTION,
  BK_EV_ENTER,
  BK_EV_LEAVE,
  BK_EV_KEY,   /* ref = "text	keysym" (UTF-8), a = mods */
  BK_EV_WHEEL, /* a = delta */
  BK_EV_SUBMIT, /* text field confirmed (Return) */
  BK_EV_VIEW,   /* scrolled view changed (nodes with "viewevents") */
  BK_EV_SCROLL, /* scrollbar request: a = 0 moveto (b = fraction), 1 units, 2 pages (b = count) */
  BK_EV_CONFIGURE, /* geometry changed ("events" nodes): a = width, b = height */
  BK_EV_KEYRELEASE /* ref = "\tkeysym", a = mods */
};

typedef struct BKHost {
  void *data;
  void (*request)(void *data);                     /* schedule a render */
  void (*set_cursor)(void *data, int cursor);
  void (*warp)(void *data, int x, int y);          /* move the pointer */
  char *(*clipboard_get)(void *data);              /* malloc'd UTF-8 or NULL */
  void (*clipboard_set)(void *data, const char *text);
  void (*schedule)(void *data, int ms, int timer); /* call BK_timer(timer) later */
  void (*emit)(void *data, BKNode *node, int kind, void *ref, double a, double b);
} BKHost;

/* Reference hooks for host objects attached to nodes/items/callbacks
 * (Python INCREF/DECREF). Optional. */
void BK_set_ref_hooks(void (*retain)(void *ref), void (*release)(void *ref));

/* -- global look ------------------------------------------------------------ */
void BK_set_scale(float scale); /* Blender's resolution scale (1 = 20 px widgets) */
float BK_scale(void);
int BK_unit(void);              /* U.widget_unit in pixels */
void BK_set_font(const char *family);
/* Themes: Blender's two presets, "dark" (Blender_Dark.xml = the built-in
 * default) and "light" (Blender_Light.xml), any case ("Blender Light" works
 * too). Every context relayouts and redraws (its host gets a request).
 * Returns 0 for an unknown name (the theme stays). Without a call the
 * BUI_THEME environment variable (dark|light) picks the first theme. */
int BK_set_theme(const char *name);
const char *BK_theme_name(void); /* "dark" or "light" */
/* A color of the active theme as RGBA 0..1: "region_bg", "header_bg",
 * "tabs_bg", "panel_header", "panel_back", "panel_sub_back", "panel_outline",
 * "text", "text_hi", "emboss", "cursor", "selection", "file_back",
 * "file_text", "file_header", "file_row_alternate", "node_back", "node_grid",
 * "node_wire", "node_body", "node_outline", "node_selected", "node_active",
 * "area_header", or
 * "<set>.<color>" for a widget color set (regular tool toggle radio option
 * textfield num numslider menu pulldown menu_back menu_item list_item box
 * scroll progress tooltip tab pie_menu curve; outline outline_sel inner
 * inner_sel item text text_sel), e.g. "regular.inner". 0 when unknown. */
int BK_theme_color(const char *key, float r_rgba[4]);
int BK_icon_id(const char *name); /* 0 when unknown */
int BK_icon_count(void);
const char *BK_icon_name(int id);
/* Every icon name (Blender's SVG icons, then BUI-only ones), for listings. */
int BK_icon_list_count(void);
const char *BK_icon_list_name(int i);

/* -- nodes -------------------------------------------------------------------- */
/* Types: column row grid box panel tabs sidetabs scroll label separator
 * progress button toggle checkbox decorator text search hotkey number slider
 * dropdown enum swatch popover menubutton menubar list tree gridview ramp
 * curve normal histogram radio textarea meter custom (see "custom nodes" below) */
BKNode *BK_node_new(const char *type);
void BK_node_free(BKNode *node);
const char *BK_node_type(const BKNode *node);
void BK_node_set_ref(BKNode *node, void *ref); /* host object (not retained) */
void *BK_node_ref(const BKNode *node);
int BK_node_add(BKNode *parent, BKNode *child, int index); /* index -1: append */
void BK_node_remove(BKNode *parent, BKNode *child);
int BK_node_child_count(const BKNode *node);
BKNode *BK_node_child(const BKNode *node, int i);
BKNode *BK_node_parent(const BKNode *node);
void BK_node_rect(const BKNode *node, int r_rect[4]);
void BK_node_natural(BKNode *node, int r_size[2]);
void BK_node_invalidate(BKNode *node); /* redraw */
void BK_node_relayout(BKNode *node);   /* size/structure changed */

/* Properties by name (see kit_props in kit_core.c for the list). Return 0 when
 * the key is unknown for this node.
 *
 * "html" (number, any node, default 0): 1 switches the node to the browser
 * look — Chrome's default form controls (2020+ refresh, Windows, light) —
 * for drawing, natural size and interaction; 0 keeps Blender's. Values,
 * events and the other properties behave the same in both. It may change at
 * any time (the node relayouts). Classes with a browser look: button, toggle
 * (as a button, pressed while on), checkbox, radio, text, search, number
 * (typed, with spin buttons), slider (<input type=range>: absolute drag),
 * dropdown (<select>; its popup too), swatch (<input type=color>), progress
 * ("factor" < 0: indeterminate), meter, textarea. Others ignore it.
 * Natural sizes at scale 1: text/number 153x21, button text+16 x 21,
 * checkbox/radio 20x19 (13px box + 3px 3px 3px 4px margins, label after it),
 * range 129x16, select widest+24 x 19, color 50x27, progress 160x16,
 * meter 80x16, textarea 182 x (rows*15+6) with rows 2 unless "rows" is set.
 * Dropdown items starting with '\x01' are <optgroup> labels in the html look
 * (bold, not selectable, the following options indented).
 * "meter" nodes: value in min..max (default 0..1) plus "low", "high",
 * "optimum" (the HTML meter's green / yellow / red regions). */
int BK_set_num(BKNode *node, const char *key, double value);
int BK_set_str(BKNode *node, const char *key, const char *value);
int BK_set_color(BKNode *node, const char *key, const float rgba[4]);
int BK_set_strs(BKNode *node, const char *key, const char *const *values, int n);
double BK_get_num(BKNode *node, const char *key);
const char *BK_get_str(BKNode *node, const char *key);
int BK_get_color(BKNode *node, const char *key, float r_rgba[4]);
int BK_strs_count(BKNode *node, const char *key);
const char *BK_strs_get(BKNode *node, const char *key, int i);
/* Link number fields edited together with Alt (vector fields). */
void BK_link_fields(BKNode *const *fields, int n);

/* Color ramp / curve / histogram data. */
int BK_ramp_count(BKNode *ramp);
void BK_ramp_get(BKNode *ramp, int i, float *r_pos, float r_rgba[4]);
void BK_ramp_set(BKNode *ramp, const float *pos, const float (*rgba)[4], int n);
int BK_ramp_active(BKNode *ramp);
void BK_ramp_evaluate(BKNode *ramp, float t, float r_rgba[4]);
int BK_curve_count(BKNode *curve, int channel);
void BK_curve_get(BKNode *curve, int channel, int i, float r_xy[2]);
void BK_curve_set(BKNode *curve, int channel, const float (*xy)[2], int n);
float BK_curve_evaluate(BKNode *curve, int channel, float x);
void BK_ramp_set_active_color(BKNode *ramp, const float rgba[4]);
void BK_histogram_set(BKNode *hist, const float *bins, int n, int channels);
/* Grid view preview colors (alpha 0 = use the item's icon). */
void BK_gridview_set_colors(BKNode *grid, const float (*colors)[4], int n);

/* Tree items (owned by their host ref; trees retain the refs). */
BKTreeItem *BK_tree_item_new(const char *label, int icon);
void BK_tree_item_free(BKTreeItem *item);
void BK_tree_item_set_ref(BKTreeItem *item, void *ref);
void *BK_tree_item_ref(const BKTreeItem *item);
void BK_tree_item_add(BKTreeItem *parent, BKTreeItem *child);
void BK_tree_item_clear(BKTreeItem *parent); /* detach (and release) all children */
void BK_tree_item_set(BKTreeItem *item, const char *key, double value);
double BK_tree_item_get(const BKTreeItem *item, const char *key);
void BK_tree_item_set_label(BKTreeItem *item, const char *label);
const char *BK_tree_item_label(const BKTreeItem *item);
int BK_tree_item_child_count(const BKTreeItem *item);
BKTreeItem *BK_tree_item_child(const BKTreeItem *item, int i);
void BK_tree_set_roots(BKNode *tree, BKTreeItem *const *roots, int n);
BKTreeItem *BK_tree_selected(BKNode *tree);
void BK_tree_select(BKNode *tree, BKTreeItem *item);
int BK_tree_row_count(BKNode *tree);
BKTreeItem *BK_tree_row(BKNode *tree, int row);
void BK_tree_row_rect(BKNode *tree, int row, int r_rect[4]);

/* Menus. A spec array describes items; nested submenus via `sub`. */
typedef struct BKMenuSpec {
  const char *text;     /* NULL or "-" = separator */
  int icon;
  const char *shortcut;
  int checked;          /* -1 none, 0/1 checkbox */
  int enabled;
  void *ref;            /* callable for BK_EV_CALL (retained while open) */
  const struct BKMenuSpec *sub;
  int nsub;
  void (*fn)(void *data); /* C callback (built-in menus), used when set */
  void *data;
} BKMenuSpec;
void BK_menubutton_set_items(BKNode *button, const BKMenuSpec *items, int n);
void BK_open_menu(BKContext *ctx,
                  BKNode *owner,
                  const BKMenuSpec *items,
                  int n,
                  const char *title,
                  int at_x,
                  int at_y);
void BK_open_pie(BKContext *ctx, const BKMenuSpec *items, int n, const char *title);

/* -- geometry managers (children of a "frame") ------------------------------------
 * Tk's pack / grid / place, also used for Qt layouts. Keys (numbers):
 *  pack:  side (0 top, 1 bottom, 2 left, 3 right), fill (1 x, 2 y, 3 both),
 *         expand, anchor (BK_ANCHOR_*), padx0 padx1 pady0 pady1 ipadx ipady
 *  grid:  row column rowspan columnspan sticky (BK_ANCHOR_* bits) + pads
 *  place: x y relx rely width height relwidth relheight anchor
 * Any manager: reqwidth / reqheight override the natural size (Tk width=). */
enum { BK_GEOM_NONE = 0, BK_GEOM_PACK, BK_GEOM_GRID, BK_GEOM_PLACE };
enum { BK_ANCHOR_N = 1, BK_ANCHOR_S = 2, BK_ANCHOR_W = 4, BK_ANCHOR_E = 8 };
void BK_geom_set_manager(BKNode *child, int manager);
int BK_geom_manager(const BKNode *child);
void BK_geom_set(BKNode *child, const char *key, double value);
double BK_geom_get(const BKNode *child, const char *key);
/* Frame grid weights (Tk columnconfigure / Qt setColumnStretch). */
void BK_frame_grid_weight(BKNode *frame, int column_axis, int index, double weight, int minsize);

/* -- images (kit/intern/image: Blender's imbuf design) ----------------------------------
 * One decoder per format for every binding: PNG, GIF (animated), BMP, TGA, QOI, PNM
 * (Wuffs), JPEG (libjpeg-turbo, DCT-scaled thumbnails, EXIF orientation), WebP
 * (libwebp: lossy, lossless, animated), Radiance HDR, SVG (nanosvg; BUI_IMAGE_SVG).
 * Pixels are premultiplied 0xAARRGGBB, decoded at the size they are drawn.
 * Synchronous decodes run on any thread; the cache and loader are UI-thread APIs. */
typedef struct BKImBuf BKImBuf;
enum {
  BK_IMAGE_NONE = 0,
  BK_IMAGE_PNG,
  BK_IMAGE_JPEG,
  BK_IMAGE_WEBP,
  BK_IMAGE_GIF,
  BK_IMAGE_BMP,
  BK_IMAGE_TGA,
  BK_IMAGE_QOI,
  BK_IMAGE_HDR,
  BK_IMAGE_PNM,
  BK_IMAGE_SVG,
  BK_IMAGE_VIDEO, /* a frame of a BKVideo */
};
/* CSS object-fit */
enum { BK_FIT_FILL = 0, BK_FIT_CONTAIN, BK_FIT_COVER, BK_FIT_NONE, BK_FIT_SCALE_DOWN };
typedef struct BKImageInfo {
  int format; /* BK_IMAGE_* */
  int width, height, frames;
} BKImageInfo;
/* Header only (no pixels): 0 when the data is not an image BUI reads. */
int BK_image_probe(const void *data, size_t len, BKImageInfo *r_info);
int BK_image_probe_file(const char *path, BKImageInfo *r_info);
const char *BK_image_format_name(int format); /* "png", NULL for unknown */
/* Decode (any thread). max_w/max_h: fit within, never upscaled; 0 = no limit.
 * NULL on failure. Free with BK_imbuf_free. */
BKImBuf *BK_imbuf_decode(const void *data, size_t len, int max_w, int max_h);
BKImBuf *BK_imbuf_load(const char *path, int max_w, int max_h);
BKImBuf *BK_imbuf_from_rgba(const unsigned char *rgba, int w, int h, int stride); /* straight RGBA */
BKImBuf *BK_imbuf_from_argb(const uint32_t *argb, int w, int h); /* premultiplied */
void BK_imbuf_free(BKImBuf *ib);
int BK_imbuf_size(const BKImBuf *ib, int *r_w, int *r_h); /* returns the frame count */
int BK_imbuf_format(const BKImBuf *ib);
const uint32_t *BK_imbuf_pixels(const BKImBuf *ib, int frame); /* premultiplied 0xAARRGGBB */
int BK_imbuf_delay(const BKImBuf *ib, int frame); /* ms; 0 for stills */
int BK_imbuf_loops(const BKImBuf *ib);            /* 0 = forever */
int BK_imbuf_rgba(const BKImBuf *ib, int frame, unsigned char *dst); /* straight RGBA, w*h*4 */
/* Frame `frame` alone (GIF / animated WebP decode up to it; stills: frame 0). */
BKImBuf *BK_imbuf_decode_frame(const void *data, size_t len, int frame, int max_w, int max_h);
/* A BUI_texture_* (RGBA) holding a frame, for Dear ImGui and textured meshes;
 * free with BUI_texture_free. 0 on failure. */
int BK_imbuf_texture(const BKImBuf *ib, int frame);
/* Replaces texture `tex` (same size) with a frame: an animation's next one. */
int BK_imbuf_texture_update(const BKImBuf *ib, int frame, int tex);

/* An animation stepped by the caller (GIF, animated WebP): one frame at a
 * time at the size drawn (fit within max_w x max_h; 0 = free), looping.
 * The data is copied. NULL when it is not an animation BUI reads. */
typedef struct BKAnim BKAnim;
BKAnim *BK_anim_open(const void *data, size_t len, int max_w, int max_h);
/* The next frame (yours: BK_imbuf_free) and how long to show it; NULL on errors. */
BKImBuf *BK_anim_next(BKAnim *anim, int *r_delay_ms);
/* Intrinsic size and loop count (0 = forever); returns the frame count. */
int BK_anim_info(const BKAnim *anim, int *r_w, int *r_h, int *r_loops);
void BK_anim_free(BKAnim *anim);

/* -- video (kit/intern/image: one player for GIF, WebP and video) ---------------------
 * A <video>: decoded by the OS (Media Foundation, AVFoundation, GStreamer) or
 * streamed from the file at
 * the size it is drawn, frames in one GPU texture updated in place (NV12:
 * YCbCr converted in the shader; the CPU raster converts what it draws).
 * It loads when first drawn and plays only while it shows: off screen,
 * clipped, in a hidden window, nothing is decoded. Muted (no audio yet).
 * UI-thread API; the state listener runs on the UI thread. */
typedef struct BKVideo BKVideo;
enum { BK_VIDEO_LOADING = 0, BK_VIDEO_PLAYING, BK_VIDEO_PAUSED, BK_VIDEO_ENDED, BK_VIDEO_ERROR };
/* Autoplays (a muted autoplay, as browsers allow); BK_video_pause before the
 * first draw shows the first frame instead. NULL for an empty src. */
BKVideo *BK_video_new(const char *src);
void BK_video_free(BKVideo *v);
void BK_video_play(BKVideo *v); /* an ended video starts over */
void BK_video_pause(BKVideo *v);
void BK_video_seek(BKVideo *v, double seconds); /* the frame there shows, paused or not */
void BK_video_set_rate(BKVideo *v, double rate); /* playback speed, 1/16 .. 16 */
void BK_video_set_loop(BKVideo *v, int loop);
/* An image file shown until the first frame (HTML poster); NULL: none. */
void BK_video_set_poster(BKVideo *v, const char *src);
/* Blender-style controls over it (play/pause, scrubber, time) in "video" nodes. */
void BK_video_set_controls(BKVideo *v, int on);
int BK_video_state(const BKVideo *v);        /* BK_VIDEO_* */
double BK_video_time(const BKVideo *v);      /* seconds */
double BK_video_duration(const BKVideo *v);  /* seconds; 0 until loaded or unknown */
int BK_video_size(const BKVideo *v, int *r_w, int *r_h); /* intrinsic; 0 until loaded */
/* Called on every state change (loaded, playing, paused, ended, error). */
void BK_video_set_listener(BKVideo *v, void (*fn)(BKVideo *v, int state, void *data), void *data);
/* Dear ImGui and textured meshes: the current frame in a BUI_texture (RGBA),
 * created when `tex` is 0 (or the size changed: the old one is freed) and
 * updated in place when a new frame came; returns the texture (0 before
 * the first frame). Calling it each frame is what keeps it playing: the
 * host keeps rendering while BK_video_state is BK_VIDEO_PLAYING. */
int BK_video_texture(BKVideo *v, int tex, int max_w, int max_h);
/* Shown on a "video" node (fitted, with its controls) or an "image" node; the
 * node borrows it (free the video after the node lets go: set NULL). */
void BK_node_set_video(BKNode *node, BKVideo *v, int fit);
/* The backends this build reads video with ("mf", "avfoundation", "gstreamer"),
 * comma-separated; "" when built without video. */
const char *BK_video_backends(void);

/* The shared cache and loader. Sources (file paths) decode on a few worker
 * threads at the size they are drawn, visible first; results arrive on the UI
 * thread (the window loop wakes for them) and redraw only where they show. */
typedef struct BKImageStats {
  size_t bytes, budget; /* decoded pixels held / the budget */
  int entries, pending; /* cached entries, decodes queued or running */
  int decoded, failed, cancelled, evicted; /* since start (or BK_image_exit) */
  double decode_ms; /* time the workers spent decoding, since the process started */
} BKImageStats;
void BK_image_set_budget(size_t bytes); /* default 256 MiB */
void BK_image_set_threads(int n);       /* 0 = cores - 1, at most 4; before the first request */
/* Called on a worker thread when results are ready (default: BK_window_wake).
 * Hosts with their own loop call BK_image_poll from it. */
void BK_image_set_notify(void (*notify)(void *data), void *data);
int BK_image_poll(void); /* hands finished decodes to the cache; returns how many */
void BK_image_stats(BKImageStats *r_stats);
/* Intrinsic size of a source from its header (read once): layout without pixels. */
int BK_image_info(const char *src, BKImageInfo *r_info);
/* Decode ahead (lower priority than what is drawn): rows a list will show next. */
void BK_image_prefetch(const char *src, int w, int h, int fit);
void BK_image_clear_cache(void); /* drops every settled entry */
void BK_image_exit(void);        /* joins the workers, frees the cache */

/* -- canvas (Tk Canvas, QPainter) -------------------------------------------------- */
enum { BK_ITEM_RECT, BK_ITEM_OVAL, BK_ITEM_LINE, BK_ITEM_POLYGON, BK_ITEM_TEXT, BK_ITEM_ARC,
       BK_ITEM_IMAGE, BK_ITEM_ROUNDRECT };
typedef struct BKCanvasItem {
  int id, type;
  const float *coords; /* x0 y0 x1 y1 ... in canvas space */
  int ncoords;
  float fill[4], outline[4]; /* alpha 0 = none */
  float width, radius, size; /* line width, corner radius, text size (0 = UI) */
  float start, extent;       /* arcs, degrees */
  const char *text;
  int anchor;                /* BK_ANCHOR_* bits, 0 = center */
  const unsigned char *rgba; /* images: iw * ih RGBA pixels */
  int iw, ih;
  int hidden;
  int text_flags;  /* BUI_FONT_BOLD | ITALIC | UNDERLINE */
  int clip_on;     /* clip the item to `clip` (x, y, w, h in canvas space) */
  float clip[4];
  const char *family; /* text font family; NULL = the UI font. Not copied:
                         it must outlive the item (e.g. a static string) */
  /* Images: exactly one of rgba (copied), imbuf (borrowed: must outlive the
   * item) or src (a file, decoded through the cache at the box size). With
   * 4 coords the image is fitted into the box x0 y0 x1 y1 by `fit`; with 2 it
   * is drawn at its intrinsic size, placed by `anchor`. */
  const BKImBuf *imbuf;
  const char *src;
  int fit;   /* BK_FIT_* */
  int frame; /* animated src: 0 plays (each frame for its delay, looping as the file
              * says; paused while off screen); n > 0 shows frame n - 1 */
  BKVideo *video; /* images: a video's current frame (borrowed, as imbuf) */
} BKCanvasItem;
void BK_canvas_set_item(BKNode *canvas, const BKCanvasItem *item); /* insert or replace by id */
void BK_canvas_add_item(BKNode *canvas, const BKCanvasItem *item);
/* Immediate-mode hosts that rebuild the whole list each frame: overwrite the
 * item at `index` (index == count appends), reusing its buffers, then call
 * BK_canvas_truncate with the new count (drops the rest, redraws once). */
void BK_canvas_put_item(BKNode *canvas, int index, const BKCanvasItem *item);
void BK_canvas_truncate(BKNode *canvas, int count); /* append, ids not checked */
void BK_canvas_delete(BKNode *canvas, int id);                     /* id < 0: all */
void BK_canvas_raise(BKNode *canvas, int id, int to_top);
int BK_canvas_count(BKNode *canvas);

/* Images on labels/buttons (drawn at their size) and "image" nodes (fitted):
 * straight RGBA (copied), a borrowed BKImBuf, or a source decoded through the cache. */
void BK_node_set_image(BKNode *node, const unsigned char *rgba, int w, int h);
void BK_node_set_imbuf(BKNode *node, const BKImBuf *ib);
void BK_node_set_image_source(BKNode *node, const char *src, int fit);
/* Animated sources play (0, the default) or show frame n - 1 (n > 0: paused). */
void BK_node_set_image_frame(BKNode *node, int frame);

/* -- custom nodes ("custom") ------------------------------------------------------------
 * A node whose drawing and input belong to the host: other UI systems embedded
 * in the kit (Dear ImGui, bindings/imgui) or hand-written editors. It gets
 *  - every pointer event over it, for all three buttons (several may be held),
 *    with the pointer grabbed from a press until the last release (motion keeps
 *    arriving outside the node, before any drag threshold);
 *  - precise wheel deltas (BK_scroll), vertical and horizontal;
 *  - keyboard focus when clicked (until another widget is clicked or
 *    BK_focus_node moves it), then every key press, typed text and key release
 *    (also while merely hovered, like other widgets);
 *  - a `frame` call at the start of BK_render when it asked for one
 *    (BK_custom_request_frame), its size changed or it was never drawn: the
 *    place to update state and BK_node_invalidate() itself (an immediate-mode
 *    frame). Requests while a frame runs are honored by BK_animating(), so
 *    window hosts render the next one paced to the display;
 *  - `draw` with the raster, clipped to the redrawn region inside the node.
 *    It may be called several times per render (one per damaged region) and
 *    for regions other widgets damaged, so it must draw the current state
 *    without changing it.
 * Positions are in raster pixels relative to the node's top-left corner. */
enum {
  BK_CUSTOM_PRESS = 1, /* button: 1 left, 2 middle, 3 right */
  BK_CUSTOM_RELEASE,
  BK_CUSTOM_MOTION,
  BK_CUSTOM_LEAVE,     /* the pointer left the node (not sent while grabbed) */
  BK_CUSTOM_WHEEL,     /* wheel_x, wheel_y */
  BK_CUSTOM_KEY,       /* keysym and/or text */
  BK_CUSTOM_KEYUP,     /* keysym */
  BK_CUSTOM_FOCUS,     /* keyboard focus gained */
  BK_CUSTOM_BLUR,      /* keyboard focus lost */
};

typedef struct BKCustomEvent {
  int type;            /* BK_CUSTOM_* */
  float x, y;          /* pointer, relative to the node */
  int button;          /* PRESS / RELEASE */
  int mods;            /* BK_SHIFT | BK_CTRL | BK_ALT (| BK_BUTTON* on pointer events) */
  int clicks;          /* PRESS: 2 for a double click */
  float wheel_x;       /* notches; > 0: scroll right (wheel tilted right) */
  float wheel_y;       /* notches; > 0: scroll up (wheel turned away) */
  const char *text;    /* KEY: UTF-8 text the key typed ("" for none) */
  const char *keysym;  /* KEY / KEYUP: X11 keysym name ("Return", "a", "F1"; "" for none) */
} BKCustomEvent;

typedef struct BKCustomClass {
  /* All optional. `event` returns 1 when it used the event (keys it ignores
   * reach the kit's shortcuts). */
  int (*event)(void *data, BKNode *node, const BKCustomEvent *e);
  void (*frame)(void *data, BKNode *node);
  /* `rect` = node x, y, w, h in raster pixels; the raster's clip is already set */
  void (*draw)(void *data, BKNode *node, BuiRaster *r, const int rect[4]);
  void (*free)(void *data, BKNode *node); /* the node is being freed */
  int natural_w, natural_h;               /* natural size (0: 8 x 8 widget units) */
} BKCustomClass;

/* Attaches the callbacks (copied) and their `data` to a "custom" node. */
int BK_custom_set(BKNode *node, const BKCustomClass *cls, void *data);
void *BK_custom_data(BKNode *node);
/* Ask for a `frame` call: ms <= 0 at the next render (requested right away;
 * from inside a frame: the next display frame), else in about `ms`
 * milliseconds (through BKHost.schedule; headless loops get it at the first
 * BK_render after the time passed). */
void BK_custom_request_frame(BKNode *node, int ms);
/* Pointer shape over the node (BK_CURSOR_*), applied right away when hovered. */
void BK_custom_set_cursor(BKNode *node, int cursor);
int BK_custom_has_focus(BKNode *node);
/* The context a node is shown in (NULL while detached). */
BKContext *BK_node_context(const BKNode *node);

/* -- text area (multi-line) -----------------------------------------------------------
 * Text via BK_set_str/get_str("value"); edits are byte offsets. */
void BK_textarea_insert(BKNode *n, int offset, const char *text);
void BK_textarea_delete(BKNode *n, int start, int end);
int BK_textarea_caret(BKNode *n);
void BK_textarea_set_caret(BKNode *n, int offset);
int BK_textarea_selection(BKNode *n, int *r_start, int *r_end);
void BK_textarea_set_selection(BKNode *n, int start, int end);
/* Colored spans (Text tags / syntax highlight): start/end offsets + RGBA. */
/* Styled ranges (Tk text tags): foreground and/or background color. */
typedef struct BKTextSpan {
  int start, end; /* byte offsets */
  float fg[4], bg[4];
  int has_fg, has_bg;
} BKTextSpan;
void BK_textarea_set_spans(BKNode *n, const BKTextSpan *spans, int count);
int BK_textarea_index_at(BKNode *n, int x, int y); /* window coordinates -> byte offset */
void BK_textarea_see(BKNode *n, int offset);

/* -- tree columns ------------------------------------------------------------------------ */
void BK_tree_item_set_values(BKTreeItem *item, const char *const *values, int n);
/* Column headers via BK_set_strs(tree, "headers", ...). */

/* -- undo (Blender's Ctrl+Z / Ctrl+Shift+Z over UI value edits) ----------------------------- */
int BK_undo(BKContext *ctx);
int BK_redo(BKContext *ctx);
int BK_undo_depth(BKContext *ctx, int *r_redo);

/* -- animation (panel open/close) --------------------------------------------------------------
 * While BK_animating() is non-zero the host should keep rendering frames. */
int BK_animating(BKContext *ctx);
void BK_set_animations(int enabled); /* also animated images (off: reduced motion) */

/* -- context ------------------------------------------------------------------- */
BKContext *BK_context_new(BKNode *root);
void BK_context_free(BKContext *ctx);
void BK_context_set_host(BKContext *ctx, const BKHost *host);
void BK_context_set_header(BKContext *ctx, BKNode *header); /* fixed top bar */
BKNode *BK_context_root(BKContext *ctx);

/* Render into `r` (w x h). Redraws only damaged regions unless `full`.
 * Returns the number of redrawn regions (0 = nothing changed). */
int BK_render(BKContext *ctx, BuiRaster *r, int w, int h, int full);
int BK_needs_render(BKContext *ctx);

/* Input; each returns non-zero when something changed. */
int BK_press(BKContext *ctx, int x, int y, int mods);
int BK_release(BKContext *ctx, int x, int y, int mods);
int BK_motion(BKContext *ctx, int x, int y, int mods);
int BK_wheel(BKContext *ctx, int x, int y, int delta, int mods);
/* Precise scrolling (touchpads, tilt wheels): dx, dy in wheel notches,
 * dx > 0 right, dy > 0 up. Custom nodes receive the deltas as they are;
 * other widgets get BK_wheel steps (horizontal ones as Shift + wheel). */
int BK_scroll(BKContext *ctx, int x, int y, float dx, float dy, int mods);
int BK_key(BKContext *ctx, const char *text, const char *keysym, int mods);
int BK_context_menu(BKContext *ctx, int x, int y, int mods);
/* Raw-only input for host bindings (Tk's <KeyRelease>, <Button-2>, <ButtonRelease-3>):
 * forwarded to "events" nodes, no widget behavior. */
int BK_key_release(BKContext *ctx, const char *keysym, int mods);
int BK_button(BKContext *ctx, int x, int y, int button, int release, int mods);
void BK_timer(BKContext *ctx, int timer);
void BK_bind(BKContext *ctx, const char *combo, void *ref); /* "ctrl+s" -> BK_EV_CALL */
void BK_close_popups(BKContext *ctx);
void BK_set_pointer(BKContext *ctx, int x, int y);

/* Introspection (tests, tools). */
int BK_cursor(BKContext *ctx);
BKNode *BK_hover(BKContext *ctx);
BKNode *BK_focus(BKContext *ctx);
/* Give a text-editing node keyboard focus (starts editing, like a click);
 * NULL ends the current edit. Other nodes are ignored. */
void BK_focus_node(BKContext *ctx, BKNode *n);
char *BK_clipboard_get(BKContext *ctx); /* malloc'd UTF-8 or NULL */
void BK_clipboard_set(BKContext *ctx, const char *text);
int BK_modal_active(BKContext *ctx);
int BK_tooltip_visible(BKContext *ctx);
int BK_popup_count(BKContext *ctx);
BKNode *BK_popup(BKContext *ctx, int i);
BKNode *BK_popup_owner(BKContext *ctx, int i);
/* Menu popups: visible rows and their rects. */
int BK_menu_row_count(BKNode *menu);
const char *BK_menu_row_text(BKNode *menu, int i);
void BK_menu_row_rect(BKNode *menu, int i, int r_rect[4]);
/* Color picker popup parts: "area", "strip", "tabs", "slider0".."slider2",
 * "alpha", "hex", "dropper". */
BKNode *BK_picker_part(BKNode *picker, const char *name);

/* -- native window host (Win32, X11, macOS Cocoa; BK_window_supported() is 0 elsewhere,
 * and on macOS off the main thread: AppKit is main-thread only) -----------------------
 * The window owns a raster, drives the context (input -> render -> present
 * without leaving C) and installs the host services (cursor, warp,
 * clipboard, tooltip timer); BKHost.emit set before is kept for callbacks. */
typedef struct BKWindow BKWindow;
typedef struct BKWindowCallbacks {
  void *data;
  void (*on_timer)(void *data, BKWindow *win, int id); /* BK_window_timer fired */
  int (*on_close)(void *data, BKWindow *win);          /* return 0 to keep it open */
  /* BK_window_request_frame: called right before the frame renders, so a host
   * can rebuild its UI for exactly the frame that presents it. */
  void (*on_frame)(void *data, BKWindow *win);
} BKWindowCallbacks;
int BK_window_supported(void);
BKWindow *BK_window_new(BKContext *ctx, const char *title, int width, int height);
void BK_window_free(BKWindow *win);
void BK_window_set_callbacks(BKWindow *win, const BKWindowCallbacks *cb);
void BK_window_show(BKWindow *win);
void BK_window_close(BKWindow *win);
int BK_window_is_open(BKWindow *win);
void BK_window_set_title(BKWindow *win, const char *title);
void BK_window_set_size(BKWindow *win, int width, int height); /* logical pixels */
void BK_window_get_size(BKWindow *win, int *r_width, int *r_height);
BuiRaster *BK_window_raster(BKWindow *win);
/* GPU rendering (Direct3D 11): windows start on the GPU when
 * BUI_gpu_set_default(1) was called or BUI_GPU=1; this turns it on for one
 * window. The device is created in the background (the first frames use the
 * software raster) and the window falls back to software if it fails.
 * BK_window_gpu() is 1 once frames are drawn on the GPU. */
void BK_window_set_gpu(BKWindow *win, int on);
int BK_window_gpu(BKWindow *win);
int BK_window_frames(BKWindow *win);
void BK_window_render_now(BKWindow *win);
int BK_window_timer(BKWindow *win, int ms); /* one-shot; returns its id */
/* Ask for an animation frame: `on_frame` runs before the next render. With
 * `vsync`, consecutive frames are paced to the display's refresh. */
void BK_window_request_frame(BKWindow *win, int vsync);
void BK_window_cancel_timer(BKWindow *win, int id);
void *BK_window_handle(BKWindow *win);       /* HWND / X11 Window / NSWindow * */
/* Window-manager requests (Tk's wm): "pos" (x, y), "minsize"/"maxsize" (w, h),
 * "resizable" (w, h), "topmost" (a), "alpha" (a), "override" (a), "state"
 * (0 normal, 1 iconic, 2 withdrawn, 3 zoomed), "lift", "lower", "focus". */
void BK_window_wm(BKWindow *win, const char *key, double a, double b);
/* Queries: "pos", "state", "screen" (w, h), "pointer" (x, y), "scale". */
int BK_window_wm_get(BKWindow *win, const char *key, double *r_a, double *r_b);
/* Message loop: run until every window is closed (or BK_window_quit). */
void BK_window_run(void);
int BK_window_pump(void); /* handle pending messages; returns open windows */
void BK_window_quit(void);
void BK_window_set_stop_check(int (*stop)(void)); /* polled after each message */
/* Waking the loop from other threads (a UI thread driven by another runtime,
 * e.g. Node): BK_window_wake_init(cb) on the loop's thread, then
 * BK_window_wake() from any thread makes the loop call cb soon (wakes are
 * coalesced, and survive Win32's modal move/resize loops). With keep-alive,
 * BK_window_run keeps running with no windows open, until BK_window_quit. */
int BK_window_wake_init(void (*cb)(void));
void BK_window_wake(void);
void BK_window_set_keep_alive(int on);

/* One of Blender's widgets drawn straight into a raster, outside any BKContext: the same
 * drawing (interface_widgets.cc port, theme, Inter text) the kit's own nodes use. For
 * other toolkits drawing Blender's widgets (Dear ImGui's Blender widgets,
 * bindings/imgui/blender). Coordinates in raster pixels at BK_scale(). */
enum {
  BK_WIDGET_BUTTON = 1,   /* push button (wcol_regular), text centered */
  BK_WIDGET_TOGGLE,       /* toggle button (wcol_toggle), SELECTED = on */
  BK_WIDGET_CHECKBOX,     /* option box + text (wcol_option), SELECTED = checked */
  BK_WIDGET_RADIO,        /* round option + text, SELECTED = chosen */
  BK_WIDGET_NUMBER,       /* number field with step arrows (wcol_num) */
  BK_WIDGET_SLIDER,       /* number slider (wcol_numslider), value = fill factor */
  BK_WIDGET_TEXT,         /* text field (wcol_text) */
  BK_WIDGET_MENU,         /* menu button with the pulldown arrows (wcol_menu) */
  BK_WIDGET_PULLDOWN,     /* header pulldown (wcol_pulldown) */
  BK_WIDGET_MENU_ITEM,    /* menu row (wcol_menu_item); text2 = shortcut; value: 1/2 = check
                             box off/on at aux[0..1], 3 = submenu arrow */
  BK_WIDGET_LIST_ITEM,    /* list row (wcol_list_item) */
  BK_WIDGET_SWATCH,       /* color swatch, color = rgba (checker under alpha) */
  BK_WIDGET_PROGRESS,     /* progress bar (wcol_progress), value = factor */
  BK_WIDGET_TAB,          /* tab (wcol_tab), SELECTED = the current tab */
  BK_WIDGET_PANEL_HEADER, /* panel header: disclosure arrow + title, OPEN */
  BK_WIDGET_DISCLOSURE,   /* tree row: hover/selected row, disclosure triangle, text, OPEN;
                             aux[0] = triangle center x (0: no triangle) */
  BK_WIDGET_MENU_BACK,    /* menu / popup backdrop with its shadow */
  BK_WIDGET_TOOLTIP_BACK, /* tooltip backdrop */
  BK_WIDGET_SEPARATOR,    /* separator line (horizontal unless h > w) */
  BK_WIDGET_LABEL,        /* text alone, at text_rect, color (a < 0: theme text) */
  BK_WIDGET_ARROW,        /* push button with a triangle, value = 0 right 1 down 2 left 3 up */
  BK_WIDGET_BOX,          /* box backdrop (wcol_box) */
};
enum {
  BK_WS_HOVER = 1 << 0,
  BK_WS_PRESSED = 1 << 1,
  BK_WS_SELECTED = 1 << 2,
  BK_WS_DISABLED = 1 << 3,
  BK_WS_EDITING = 1 << 4,    /* text being typed (text field look) */
  BK_WS_HOVER_LEFT = 1 << 5, /* number field: over the left / right step arrow */
  BK_WS_HOVER_RIGHT = 1 << 6,
  BK_WS_MIXED = 1 << 7,      /* checkbox: indeterminate */
  BK_WS_FLAT = 1 << 8,       /* no emboss: backdrop on hover only */
  BK_WS_OPEN = 1 << 9,       /* panel header / tree row expanded */
};
/* Corners for aligned groups (Blender's CNR_*): top-left 1, top-right 2, bottom-right 4,
 * bottom-left 8; BK_CORNERS_ALL for a lone widget. */
enum { BK_CORNERS_ALL = 15 };
typedef struct BKWidgetDraw {
  int kind, state, corners;
  int align;          /* text alignment: 0 left, 1 center, 2 right; -1 the widget's own */
  float rect[4];      /* x, y, w, h */
  float clip[4];      /* x0, y0, x1, y1 narrowing the raster's clip; x1 <= x0: none */
  float text_rect[4]; /* where text goes for LABEL / LIST_ITEM / MENU_ITEM / DISCLOSURE;
                         w <= 0: the widget's own text placement */
  float value;
  float color[4];     /* SWATCH color; LABEL text color (a < 0: theme) */
  float aux[4];
  const char *text;   /* label (UTF-8, may be NULL) */
  const char *text2;  /* NUMBER / SLIDER: value text drawn right of `text`; MENU_ITEM: shortcut */
} BKWidgetDraw;
void BK_draw_widget(BuiRaster *r, const BKWidgetDraw *w);
/* Width of `s` (len < 0: NUL-terminated) in the UI font at BK_scale(). */
float BK_text_width(const char *s, int len);

/* Evaluate Blender-style number input ("2*pi", "1/3 + 2^2"): Blender's
 * BLI_expr_pylike_eval (+ - * / // % ** ^, comparisons, and/or/not, x if c
 * else y, pi e tau, min max sin cos sqrt atan2 floor ...). */
int BK_eval_number(const char *expr, double *r_value);

/* -- file browser (Blender's File Browser, editors/space_file) -------------------------------
 * BK_node_new("filebrowser") is the whole browser: bookmark panels (Volumes, System,
 * Bookmarks, Recent), the directory-path bar, the file list (Name / Date Modified / Size,
 * click a header to sort) and the execute bar. Put it in any window; it fills its rect.
 *  strings  "dir", "file" (a name, or a path whose folder becomes "dir"), "button" (execute
 *           label), "search", "bookmarks_file" (default $XDG_CONFIG_HOME/bui/bookmarks.txt,
 *           %APPDATA%\bui\bookmarks.txt, or $BUI_BOOKMARKS); get "path" (first result)
 *  numbers  "mode" (0 open, 1 save, 2 directory), "multiple", "hidden", "filter" (index),
 *           "sort" (0 name, 1 date, 2 size), "invert"; get "state" (0 browsing, 1 chosen,
 *           -1 cancelled), "count", "selected"; set "execute", "cancel", "refresh" to act
 *  lists    "filters": one "Label\tpattern pattern ..." per entry ("*.py", or ".py");
 *           get "result": the chosen paths
 *  events   BK_EV_SUBMIT (a = state) when chosen or cancelled, BK_EV_CHANGE on a folder change
 * Keys: Return opens / executes, BackSpace or Alt+Up parent, Alt+Left / Alt+Right history,
 * Ctrl+H hidden files, Ctrl+B bookmark the folder, Delete on a bookmark removes it, I new
 * folder, F2 rename, R refresh, A / Alt+A (de)select all with "multiple", Escape cancels.
 *
 * BK_file_dialog runs one in its own native window until it is answered: returns the number
 * of chosen paths (0 cancelled, -1 no window host), stored NULL-terminated in *r_paths
 * (free with BK_file_dialog_free). `filters` as above. */
int BK_file_dialog(const char *title,
                   int mode,
                   const char *dir,
                   const char *file,
                   const char *const *filters,
                   int nfilters,
                   int multiple,
                   char ***r_paths);
void BK_file_dialog_free(char **paths);

/* Menu search ranks with Blender's string search (word initials, typos);
 * items chosen from a search rank first for short queries (Blender's recent
 * searches). On by default, kept in memory; load/save use one line per item. */
void BK_recent_searches_enable(int enabled);
void BK_recent_searches_clear(void);
void BK_recent_searches_load(const char *path);
void BK_recent_searches_save(const char *path);

#ifdef __cplusplus
}
#endif
