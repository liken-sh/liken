/*
 * The state that the three parts of liken-layout.so share. liken-layout.c
 * is the executor of the operator's control protocol, motion.c is the
 * animation engine that moves, fades, and scales surfaces on each
 * repaint of an output, and arrangement.c is the liken_arrangement_v1
 * protocol through which one client of a claim arranges the claim's
 * surfaces inside its region.
 */
#ifndef LIKEN_LAYOUT_H
#define LIKEN_LAYOUT_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <time.h>

#include <libweston/libweston.h>

#include "ivi-layout-export.h"

/* A connector name and a socket name are both short. 64 bytes holds
 * "wayland-" plus a claim UID, which is the longest name the operator
 * sends. */
#define NAME_LEN 64

/* The socket every pod prepared before per-claim sockets still holds
 * in WAYLAND_DISPLAY. A surface on it belongs to no claim. */
#define SHARED_SOCKET "wayland-0"

/* The name the capture socket's clients report as. */
#define CAPTURE_SOCKET "wayland-capture"

/* An xdg app_id is a short reverse-DNS name. A longer one is cut. */
#define APP_ID_LEN 128

/* A rectangle in floating point, so that a motion can stand between two
 * whole-pixel rectangles. The unit depends on the use: output-local
 * logical pixels for a region, or for a rectangle inside one. */
struct rectf {
	double x, y, w, h;
};

/* One motion's clock: a pure function of the time of an output's last
 * page flip. The start is the flip time of the first frame that
 * evaluates the motion, because a request arrives between frames and
 * the flip before it is already in the past. */
struct motion {
	bool running;
	bool started;
	struct timespec start;
	uint32_t ms;
};

/* One ivi layer per output, the size of the output. Every surface on
 * the output goes in this layer, and the layer's render order is the
 * stacking order. */
struct output_layer {
	struct weston_output *output;
	struct ivi_layout_layer *layer;
	/* The black view under every surface on this output. weston
	 * composes only the views it holds and clears nothing, so a
	 * region no surface covers would keep the pixels of the frame
	 * before it. ivi-shell ships no background of its own. */
	struct weston_curtain *background;
	struct wl_list stack; /* struct layout_surface::stack_link, bottom first */
	/* The engine's hook on this output's repaint. Its link is empty
	 * while nothing on the output moves. */
	struct weston_animation anim;
	struct wl_list link;
};

/* What a place or a hide asked the next commit to animate. */
enum pending_motion {
	PENDING_NONE,
	PENDING_CUT, /* stop every motion: the place or hide said none */
	PENDING_FADE_IN,
	PENDING_FADE_OUT,
	PENDING_MOVE,
};

struct layout_surface {
	struct ivi_layout_surface *ivisurf;
	uint32_t id;
	char socket_name[NAME_LEN];
	int32_t width, height;
	struct output_layer *placed;
	struct wl_list stack_link;
	struct wl_list link;

	/* The motion that the operator's next commit starts. */
	enum pending_motion pending;
	uint32_t pending_ms;
	struct rectf pending_from;

	/* A fade between two opacities. A fade out ends in a hide. */
	struct motion fade;
	double alpha_from, alpha_to;
	bool hiding;

	/* A glide from one region to the region ivi-layout holds now. */
	struct motion move;
	struct rectf move_from;

	/* The xdg app_id, as the module last reported it, or empty. */
	char app_id[APP_ID_LEN];

	/* Where the claim's arrangement found the surface at its last
	 * commit, in logical pixels of the region and in opacity, and the
	 * delay of the entry that names it. A surface that arrived after
	 * the commit has no start and shows at its target. */
	bool arr_from_valid;
	struct rectf arr_from;
	double arr_alpha_from;
	uint32_t arr_delay;
	/* The size of the one configure a resize entry sends when it
	 * lands, and the size the arrangement last configured, both zero
	 * when there is none. */
	int32_t arr_configure_w, arr_configure_h;
	int32_t arr_configured_w, arr_configured_h;

	/* The engine's transform, at the tail of the top-level view's
	 * transformation list while the surface shows anywhere but its
	 * region. view is the view the node is linked into, or NULL. */
	struct weston_view *view;
	struct weston_transform node;
	struct wl_listener view_destroy;

	/* The arrangement's transforms on the views of the surface's
	 * subsurfaces, struct child_node::link, and the listener that
	 * reads the app_id and finds a subsurface at each commit. */
	struct wl_list child_nodes;
	struct wl_listener commit;
};

extern const struct ivi_layout_interface *ivi;
extern struct weston_compositor *compositor;
extern struct wl_list output_layers;   /* struct output_layer::link */
extern struct wl_list layout_surfaces; /* struct layout_surface::link */

/* liken-layout.c */
void send_line(const char *format, ...) __attribute__((format(printf, 1, 2)));
void apply_stack(struct output_layer *ol);
void surface_committed(struct layout_surface *s);

/* arrangement.c */
void control_word(char *out, size_t len, const char *in);
const char *origin_of_client(struct wl_client *client);
struct output_layer *output_layer_named(const char *connector);
const char *connector_of_socket(const char *socket_name);

/* motion.c */
double motion_progress(const struct motion *m, const struct timespec *now);
void motion_begin(struct motion *m, uint32_t ms);
void motion_wake_all(void);
void layout_commit(void);
void motion_refresh_all(void);
void motion_place(struct layout_surface *s, struct output_layer *ol,
		  const char *word, uint32_t ms);
bool motion_hide(struct layout_surface *s, const char *word, uint32_t ms);
void motion_output_added(struct output_layer *ol);
void motion_output_dropped(struct output_layer *ol);
void motion_surface_left(struct layout_surface *s);
void motion_surface_added(struct layout_surface *s);
void motion_surface_removed(struct layout_surface *s);
struct weston_view *top_view_of(struct layout_surface *s);
struct rectf region_of(struct layout_surface *s, const struct timespec *now);

/* arrangement.c */

/* Where an arrangement shows one surface at one frame. rect is in
 * logical pixels of the surface's committed region, and opacity
 * multiplies any fade the operator runs. split says that the surface
 * keeps filling the region and the rectangle moves its direct-child
 * subsurfaces. */
struct arranged {
	struct rectf rect;
	double opacity;
	bool split;
};

bool arrangement_init(void);
void arrangement_place(struct layout_surface *s, const struct timespec *now,
		       struct arranged *out);
void arrangement_order(struct layout_surface **stack, int count);
void arrangement_frame_begin(struct output_layer *ol, const struct timespec *now);
bool arrangement_output_busy(struct output_layer *ol);
void arrangement_frame_done(struct output_layer *ol, const struct timespec *now);
void arrangement_output_dropped(struct output_layer *ol);
void arrangement_layout_changed(void);
void arrangement_surfaces_changed(const char *socket_name);
void arrangement_send_state(void);

#endif
