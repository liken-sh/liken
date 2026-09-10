/*
 * liken-layout.so is the operator's ivi-shell controller. weston loads
 * it from the modules= list in [core], and it finds the shell through
 * ivi_layout_get_api. weston 14 has no ivi-module= key.
 *
 * The module is an executor. It creates one ivi layer and one black
 * background view per output, opens and closes one Wayland listening
 * socket per claim, reports every surface it sees, and places surfaces
 * where the operator tells it to. It holds no layout of its own and
 * makes no decision. A surface the operator has not placed is not
 * visible, and when the operator's connection drops the last committed
 * layout stays on screen.
 *
 * The module binds each claim's listening socket itself and hands the
 * descriptor to wl_display_add_socket_fd, rather than naming the socket
 * to wl_display_add_socket. A socket the module binds has no lock file,
 * so a claim that is unprepared and prepared again gets its socket back
 * inside one compositor lifetime.
 *
 * The control protocol is lines of text on a Unix stream socket, one
 * request and one reply per line.
 * plans/completed/17-a-layout-for-every-screen.md states it, and
 * plans/completed/18-a-surface-leaves-with-a-fade.md states the
 * transition a hide carries.
 */
#define _GNU_SOURCE

#include <errno.h>
#include <limits.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#include <libweston/libweston.h>
#include <libweston/shell-utils.h>

#include "ivi-layout-export.h"

/* A connector name and a socket name are both short. 64 bytes holds
 * "wayland-" plus a claim UID, which is the longest name the operator
 * sends. */
#define NAME_LEN 64

/* The most surfaces one order request may name. A screen holds a
 * handful of surfaces, and the cap keeps the request on the stack. */
#define ORDER_MAX 64

/* The longest line either way. An order request is the longest one:
 * a sequence number, a connector name and ORDER_MAX surface ids. */
#define LINE_LEN 1024

#define DEFAULT_CONTROL_PATH "/etc/weston/layout.sock"

/* The socket every pod prepared before per-claim sockets still holds
 * in WAYLAND_DISPLAY. A surface on it belongs to no claim. */
#define SHARED_SOCKET "wayland-0"

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
	struct wl_list link;
};

struct listening_socket {
	char name[NAME_LEN];
	char connector[NAME_LEN];
	/* The descriptor libwayland accepts this name's clients on. The
	 * module binds and listens on it, then hands it to
	 * wl_display_add_socket_fd, which takes ownership: libwayland
	 * closes it when the display goes, and closing it under
	 * libwayland's event source would be unsafe. */
	int fd;
	/* A closed name keeps its entry, because libwayland has no call
	 * that removes a listener. The descriptor stays open with no
	 * path to reach it, and a client that still connects through it
	 * reports as wayland-0 rather than as the claim that is gone.
	 * A listen on a closed name binds a new descriptor at the same
	 * path and this entry holds that one.
	 * plans/open-problems/a-claims-listener-outlives-the-claim.md
	 * counts the cost. */
	bool open;
	struct wl_list link;
};

/* Which listening socket a client connected through, read once when
 * the client arrives. */
struct client_origin {
	struct wl_client *client;
	char socket_name[NAME_LEN];
	struct wl_listener destroy;
	struct wl_list link;
};

struct layout_surface {
	struct ivi_layout_surface *ivisurf;
	uint32_t id;
	char socket_name[NAME_LEN];
	int32_t width, height;
	struct output_layer *placed;
	struct wl_list stack_link;
	struct wl_list link;
};

static const struct ivi_layout_interface *ivi;
static struct weston_compositor *compositor;

/* Every output's background view goes in this one weston layer, below
 * the layer ivi-layout composes its own views in. */
static struct weston_layer background_layer;

static struct wl_list output_layers;
static struct wl_list listening_sockets;
static struct wl_list client_origins;
static struct wl_list layout_surfaces;

/* Surface ids are unique for the compositor's life, so an id the
 * operator holds never names a later surface. */
static uint32_t next_surface_id = 1;

static int control_listen_fd = -1;
static int control_fd = -1;
static struct wl_event_source *control_source;
static char control_in[LINE_LEN];
static size_t control_in_used;
static bool greeted;

static struct wl_listener configure_surface;
static struct wl_listener configure_desktop_surface;
static struct wl_listener remove_surface;
static struct wl_listener client_created;
static struct wl_listener output_created;
static struct wl_listener output_destroyed;
static struct wl_listener output_resized;

/* The control connection */

static void
drop_connection(void)
{
	if (control_source) {
		wl_event_source_remove(control_source);
		control_source = NULL;
	}
	if (control_fd >= 0) {
		close(control_fd);
		control_fd = -1;
	}
	control_in_used = 0;
	greeted = false;
}

/* Every reply and every event is one line. The descriptor is
 * non-blocking, so a write that does not complete means the operator
 * is not reading, and the connection closes. The operator replays the
 * whole layout on its next connection, so a dropped line costs one
 * reconnect and no state. */
static void
send_line(const char *format, ...)
{
	char line[LINE_LEN];
	va_list ap;
	int length;

	if (control_fd < 0)
		return;

	va_start(ap, format);
	length = vsnprintf(line, sizeof line, format, ap);
	va_end(ap);
	if (length < 0 || (size_t)length >= sizeof line) {
		weston_log("liken-layout: an event line does not fit in %zu bytes\n",
			   sizeof line);
		return;
	}

	if (send(control_fd, line, length, MSG_NOSIGNAL) != length)
		drop_connection();
}

static void
reply_ok(uint32_t seq)
{
	send_line("ok %u\n", seq);
}

static void
reply_error(uint32_t seq, const char *text)
{
	send_line("error %u %s\n", seq, text);
}

/* Token parsing */

static bool
parse_u32(const char *token, uint32_t *out)
{
	char *end;
	unsigned long value;

	if (!token || !token[0])
		return false;
	errno = 0;
	value = strtoul(token, &end, 10);
	if (errno != 0 || *end != '\0' || value > UINT32_MAX)
		return false;
	*out = (uint32_t)value;
	return true;
}

static bool
parse_i32(const char *token, int32_t *out)
{
	char *end;
	long value;

	if (!token || !token[0])
		return false;
	errno = 0;
	value = strtol(token, &end, 10);
	if (errno != 0 || *end != '\0' || value < INT32_MIN || value > INT32_MAX)
		return false;
	*out = (int32_t)value;
	return true;
}

/* Output layers */

static struct output_layer *
output_layer_of(struct weston_output *output)
{
	struct output_layer *ol;

	wl_list_for_each(ol, &output_layers, link)
		if (ol->output == output)
			return ol;
	return NULL;
}

static struct output_layer *
output_layer_named(const char *connector)
{
	struct output_layer *ol;

	wl_list_for_each(ol, &output_layers, link)
		if (strcmp(ol->output->name, connector) == 0)
			return ol;
	return NULL;
}

/* The render order is the whole stack, bottom first, because
 * layer_set_render_order replaces the layer's contents. A surface the
 * operator moved to another output leaves this layer the same way. */
static void
apply_stack(struct output_layer *ol)
{
	struct ivi_layout_surface **order;
	struct layout_surface *s;
	int count = 0;

	order = calloc(wl_list_length(&ol->stack) + 1, sizeof *order);
	if (!order)
		return;
	wl_list_for_each(s, &ol->stack, stack_link)
		order[count++] = s->ivisurf;
	ivi->layer_set_render_order(ol->layer, order, count);
	free(order);
}

static int
background_label(struct weston_surface *surface, char *buffer, size_t len)
{
	(void)surface;
	return snprintf(buffer, len, "liken-layout background");
}

/* The background covers the whole output and is opaque, so weston
 * repaints black where the operator placed nothing. */
static void
add_background(struct output_layer *ol)
{
	struct weston_curtain_params params = {
		.get_label = background_label,
		.r = 0.0f, .g = 0.0f, .b = 0.0f, .a = 1.0f,
		.pos = ol->output->pos,
		.width = ol->output->width,
		.height = ol->output->height,
	};

	ol->background = weston_shell_utils_curtain_create(compositor, &params);
	if (!ol->background) {
		weston_log("liken-layout: no background for output %s\n",
			   ol->output->name);
		return;
	}
	weston_surface_set_role(ol->background->view->surface,
				"liken-layout-background", NULL, 0);
	weston_view_move_to_layer(ol->background->view,
				  &background_layer.view_list);
	weston_view_set_output(ol->background->view, ol->output);
}

static void
drop_background(struct output_layer *ol)
{
	if (ol->background) {
		weston_shell_utils_curtain_destroy(ol->background);
		ol->background = NULL;
	}
}

/* The layer's source and destination rectangles are both the output's
 * logical size, which makes the layer transform an identity. Surface
 * rectangles are then in the output's logical pixels, and ivi-layout
 * adds the output's position in global space itself
 * (ivi-layout.c, calc_surface_to_global_matrix_and_mask_to_weston_surface). */
static void
add_output_layer(struct weston_output *output)
{
	struct output_layer *ol;

	ol = calloc(1, sizeof *ol);
	if (!ol) {
		weston_log("liken-layout: no memory for the layer of output %s\n",
			   output->name);
		return;
	}
	ol->output = output;
	/* ivi identifies a layer by an integer the controller picks, and
	 * the output's own id keys this one, so two live outputs never
	 * share a layer id. */
	ol->layer = ivi->layer_create_with_dimension(output->id,
						    output->width, output->height);
	wl_list_init(&ol->stack);
	wl_list_insert(&output_layers, &ol->link);

	ivi->screen_add_layer(output, ol->layer);
	ivi->layer_set_visibility(ol->layer, true);
	ivi->commit_changes();

	add_background(ol);
}

/* A curtain holds the size it was built with, so a resized output gets
 * a new one. */
static void
resize_output_layer(struct output_layer *ol)
{
	ivi->layer_set_source_rectangle(ol->layer, 0, 0,
					ol->output->width, ol->output->height);
	ivi->layer_set_destination_rectangle(ol->layer, 0, 0,
					     ol->output->width, ol->output->height);
	ivi->commit_changes();

	drop_background(ol);
	add_background(ol);
}

/* ivi-layout listens for output destruction before this module does,
 * and its handler already removed the layer from the screen. Calling
 * screen_remove_layer here would look up a screen that is gone. */
static void
drop_output_layer(struct weston_output *output)
{
	struct output_layer *ol = output_layer_of(output);
	struct layout_surface *s, *next;

	if (!ol)
		return;
	wl_list_for_each_safe(s, next, &ol->stack, stack_link) {
		wl_list_remove(&s->stack_link);
		wl_list_init(&s->stack_link);
		s->placed = NULL;
		ivi->surface_set_visibility(s->ivisurf, false);
	}
	drop_background(ol);
	ivi->layer_destroy(ol->layer);
	wl_list_remove(&ol->link);
	free(ol);
	ivi->commit_changes();
}

/* The operator computes pixels from the size the compositor lays out
 * in, so this reports the logical size and the scale and never the
 * kernel mode. */
static void
send_output(struct weston_output *output)
{
	send_line("output %s %d %d %d\n", output->name, output->width,
		  output->height, output->current_scale);
}

static void
on_output_created(struct wl_listener *listener, void *data)
{
	struct weston_output *output = data;

	(void)listener;
	add_output_layer(output);
	send_output(output);
}

static void
on_output_destroyed(struct wl_listener *listener, void *data)
{
	struct weston_output *output = data;

	(void)listener;
	drop_output_layer(output);
	send_line("output-gone %s\n", output->name);
}

static void
on_output_resized(struct wl_listener *listener, void *data)
{
	struct weston_output *output = data;
	struct output_layer *ol = output_layer_of(output);

	(void)listener;
	if (ol)
		resize_output_layer(ol);
	send_output(output);
}

/* Listening sockets, one per claim */

static struct listening_socket *
socket_named(const char *name)
{
	struct listening_socket *ls;

	wl_list_for_each(ls, &listening_sockets, link)
		if (strcmp(ls->name, name) == 0)
			return ls;
	return NULL;
}

/* A socket name becomes a path under XDG_RUNTIME_DIR that close
 * unlinks, so a name with a slash in it would unlink a file outside
 * that directory. */
static bool
name_is_safe(const char *name)
{
	return name && name[0] && name[0] != '.' && !strchr(name, '/') &&
	       strlen(name) < NAME_LEN;
}

static bool
socket_path(const char *name, char *out, size_t len)
{
	const char *dir = getenv("XDG_RUNTIME_DIR");
	int written;

	if (!dir || !dir[0])
		return false;
	written = snprintf(out, len, "%s/%s", dir, name);
	return written > 0 && (size_t)written < len;
}

/* wl_display_add_socket takes a flock on a lock file beside the socket
 * and holds it for the compositor's life, so it refuses a listen on a
 * name the module closed earlier. The module therefore binds the socket
 * itself and hands the descriptor over, which involves no lock file. A
 * Deployment with the Recreate strategy and a re-run Job both keep
 * their ResourceClaim, so the kubelet unprepares and prepares the same
 * claim, and both would otherwise wait for a compositor restart.
 *
 * The socket directory holds no other server, so a path left behind by
 * an earlier compositor is stale and the bind unlinks it. Losing the
 * lock file loses that check, which is why do_listen refuses the name
 * weston opened for itself. */
static int
bind_listening_socket(const char *path)
{
	struct sockaddr_un addr = { .sun_family = AF_UNIX };
	int fd;

	if (strlen(path) >= sizeof addr.sun_path) {
		weston_log("liken-layout: %s is too long for a Unix socket path\n", path);
		return -1;
	}
	snprintf(addr.sun_path, sizeof addr.sun_path, "%s", path);

	fd = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
	if (fd < 0) {
		weston_log("liken-layout: no socket for %s: %s\n", path, strerror(errno));
		return -1;
	}
	if (unlink(path) < 0 && errno != ENOENT) {
		weston_log("liken-layout: %s did not unlink: %s\n", path, strerror(errno));
		close(fd);
		return -1;
	}
	if (bind(fd, (struct sockaddr *)&addr, sizeof addr) < 0 ||
	    listen(fd, 128) < 0) {
		weston_log("liken-layout: %s does not listen: %s\n", path, strerror(errno));
		close(fd);
		return -1;
	}
	return fd;
}

static void
do_listen(uint32_t seq, char **save)
{
	const char *name = strtok_r(NULL, " ", save);
	const char *connector = strtok_r(NULL, " ", save);
	struct listening_socket *ls;
	char path[PATH_MAX];
	bool is_new = false;
	int fd;

	if (!name_is_safe(name) || !connector || !connector[0]) {
		reply_error(seq, "listen takes a socket name and a connector");
		return;
	}
	if (strcmp(name, SHARED_SOCKET) == 0) {
		reply_error(seq, "wayland-0 belongs to weston, not to a claim");
		return;
	}

	ls = socket_named(name);
	if (ls && ls->open) {
		reply_ok(seq);
		return;
	}
	if (!socket_path(name, path, sizeof path)) {
		reply_error(seq, "XDG_RUNTIME_DIR does not name the socket's directory");
		return;
	}
	if (!ls) {
		ls = calloc(1, sizeof *ls);
		if (!ls) {
			reply_error(seq, "no memory for the socket");
			return;
		}
		snprintf(ls->name, sizeof ls->name, "%s", name);
		ls->fd = -1;
		wl_list_insert(&listening_sockets, &ls->link);
		is_new = true;
	}

	fd = bind_listening_socket(path);
	if (fd < 0) {
		if (is_new) {
			wl_list_remove(&ls->link);
			free(ls);
		}
		reply_error(seq, "the module could not bind the socket");
		return;
	}
	if (wl_display_add_socket_fd(compositor->wl_display, fd) < 0) {
		close(fd);
		if (is_new) {
			wl_list_remove(&ls->link);
			free(ls);
		}
		reply_error(seq, "the compositor did not take the socket");
		return;
	}

	snprintf(ls->connector, sizeof ls->connector, "%s", connector);
	ls->fd = fd;
	ls->open = true;
	weston_log("liken-layout: listening on %s for output %s on descriptor %d\n",
		   ls->name, ls->connector, ls->fd);
	reply_ok(seq);
}

static void
do_close(uint32_t seq, char **save)
{
	const char *name = strtok_r(NULL, " ", save);
	struct listening_socket *ls;
	char path[PATH_MAX];

	if (!name_is_safe(name)) {
		reply_error(seq, "close takes a socket name");
		return;
	}
	ls = socket_named(name);
	if (!ls || !ls->open) {
		reply_ok(seq);
		return;
	}
	if (!socket_path(ls->name, path, sizeof path)) {
		reply_error(seq, "XDG_RUNTIME_DIR does not name the socket's directory");
		return;
	}
	if (unlink(path) < 0 && errno != ENOENT) {
		reply_error(seq, "the module could not unlink the socket path");
		return;
	}
	ls->open = false;
	weston_log("liken-layout: closed %s. Clients on it keep their connections, "
		   "and descriptor %d stays open with no path\n",
		   ls->name, ls->fd);
	reply_ok(seq);
}

/* Clients and the socket each one arrived on */

/* An accepted Unix socket reports the path its listener bound, so
 * getsockname on the client's descriptor names the socket the client
 * connected through. That name is the claim's identity, and nothing in
 * the client's pod can change it. Every other client, wayland-0
 * included, reports as wayland-0. */
static void
read_client_origin(struct wl_client *client, char *out, size_t len)
{
	struct sockaddr_un addr;
	socklen_t addr_len = sizeof addr;
	struct listening_socket *ls;
	const char *base;

	snprintf(out, len, "%s", SHARED_SOCKET);

	if (getsockname(wl_client_get_fd(client), (struct sockaddr *)&addr,
			&addr_len) < 0)
		return;
	if (addr.sun_family != AF_UNIX || addr.sun_path[0] == '\0')
		return;
	addr.sun_path[sizeof addr.sun_path - 1] = '\0';

	base = strrchr(addr.sun_path, '/');
	base = base ? base + 1 : addr.sun_path;

	wl_list_for_each(ls, &listening_sockets, link)
		if (ls->open && strcmp(ls->name, base) == 0) {
			snprintf(out, len, "%s", ls->name);
			return;
		}
}

static void
on_client_destroyed(struct wl_listener *listener, void *data)
{
	struct client_origin *co =
		wl_container_of(listener, co, destroy);

	(void)data;
	wl_list_remove(&co->link);
	wl_list_remove(&co->destroy.link);
	free(co);
}

static void
on_client_created(struct wl_listener *listener, void *data)
{
	struct wl_client *client = data;
	struct client_origin *co;

	(void)listener;
	co = calloc(1, sizeof *co);
	if (!co)
		return;
	co->client = client;
	read_client_origin(client, co->socket_name, sizeof co->socket_name);
	co->destroy.notify = on_client_destroyed;
	wl_client_add_destroy_listener(client, &co->destroy);
	wl_list_insert(&client_origins, &co->link);
}

static const char *
origin_of_surface(struct weston_surface *ws)
{
	struct client_origin *co;
	struct wl_client *client;

	if (!ws->resource)
		return SHARED_SOCKET;
	client = wl_resource_get_client(ws->resource);
	wl_list_for_each(co, &client_origins, link)
		if (co->client == client)
			return co->socket_name;
	return SHARED_SOCKET;
}

/* Surfaces */

static struct layout_surface *
surface_of(struct ivi_layout_surface *ivisurf)
{
	struct layout_surface *s;

	wl_list_for_each(s, &layout_surfaces, link)
		if (s->ivisurf == ivisurf)
			return s;
	return NULL;
}

static struct layout_surface *
surface_with_id(uint32_t id)
{
	struct layout_surface *s;

	wl_list_for_each(s, &layout_surfaces, link)
		if (s->id == id)
			return s;
	return NULL;
}

static struct layout_surface *
add_surface(struct ivi_layout_surface *ivisurf, struct weston_surface *ws)
{
	struct layout_surface *s = calloc(1, sizeof *s);

	if (!s)
		return NULL;
	s->ivisurf = ivisurf;
	s->id = next_surface_id++;
	s->width = ws->width;
	s->height = ws->height;
	snprintf(s->socket_name, sizeof s->socket_name, "%s",
		 origin_of_surface(ws));
	wl_list_init(&s->stack_link);
	wl_list_insert(&layout_surfaces, &s->link);

	weston_log("liken-layout: surface %u arrived on %s at %dx%d\n",
		   s->id, s->socket_name, s->width, s->height);
	return s;
}

static void
send_surface(struct layout_surface *s)
{
	send_line("surface %u %s %d %d\n", s->id, s->socket_name,
		  s->width, s->height);
}

/* Plain xdg-shell clients arrive on the desktop configure signal and
 * ivi_application clients on the ivi one, so both reach here. The
 * commit is here because a source rectangle takes effect only on one,
 * and ivi-layout draws nothing for a surface whose source rectangle is
 * still zero. */
static void
on_configure(struct wl_listener *listener, void *data)
{
	struct ivi_layout_surface *ivisurf = data;
	struct weston_surface *ws;
	struct layout_surface *s;

	(void)listener;
	ws = ivi->surface_get_weston_surface(ivisurf);
	if (!ws || !weston_surface_has_content(ws))
		return;

	s = surface_of(ivisurf);
	if (!s) {
		s = add_surface(ivisurf, ws);
		if (!s)
			return;
		send_surface(s);
	} else if (s->width != ws->width || s->height != ws->height) {
		s->width = ws->width;
		s->height = ws->height;
		send_line("surface-size %u %d %d\n", s->id, s->width, s->height);
	}

	ivi->surface_set_source_rectangle(ivisurf, 0, 0, ws->width, ws->height);
	ivi->commit_changes();
}

static void
on_remove(struct wl_listener *listener, void *data)
{
	struct layout_surface *s = surface_of(data);
	struct output_layer *ol;

	(void)listener;
	if (!s)
		return;
	ol = s->placed;
	if (ol) {
		wl_list_remove(&s->stack_link);
		s->placed = NULL;
	}
	send_line("surface-gone %u\n", s->id);
	weston_log("liken-layout: surface %u on %s is gone\n",
		   s->id, s->socket_name);
	wl_list_remove(&s->link);
	free(s);

	if (ol) {
		apply_stack(ol);
		ivi->commit_changes();
	}
}

/* Placement */

static bool
parse_transition(const char *word, uint32_t ms, enum ivi_layout_transition_type *out)
{
	if (!word)
		return false;
	if (strcmp(word, "none") == 0) {
		/* A commit resets the type to none, but a place that
		 * follows a hide with no commit between would otherwise
		 * inherit the earlier type. */
		*out = IVI_LAYOUT_TRANSITION_NONE;
		return true;
	}
	/* ivi-layout divides the elapsed time by the duration to get a
	 * transition's progress, so a fade or a move of zero
	 * milliseconds is a request the module refuses rather than
	 * passes on. */
	if (strcmp(word, "fade") == 0 && ms > 0) {
		*out = IVI_LAYOUT_TRANSITION_VIEW_FADE_ONLY;
		return true;
	}
	if (strcmp(word, "move") == 0 && ms > 0) {
		*out = IVI_LAYOUT_TRANSITION_VIEW_DEST_RECT_ONLY;
		return true;
	}
	return false;
}

static void
do_place(uint32_t seq, char **save)
{
	uint32_t id, ms;
	int32_t x, y, w, h;
	const char *connector;
	const char *word;
	enum ivi_layout_transition_type transition;
	struct layout_surface *s;
	struct output_layer *ol;

	if (!parse_u32(strtok_r(NULL, " ", save), &id)) {
		reply_error(seq, "place takes a surface id");
		return;
	}
	connector = strtok_r(NULL, " ", save);
	if (!parse_i32(strtok_r(NULL, " ", save), &x) ||
	    !parse_i32(strtok_r(NULL, " ", save), &y) ||
	    !parse_i32(strtok_r(NULL, " ", save), &w) ||
	    !parse_i32(strtok_r(NULL, " ", save), &h)) {
		reply_error(seq, "place takes x y w h");
		return;
	}
	if (w <= 0 || h <= 0) {
		reply_error(seq, "place takes a width and a height above zero");
		return;
	}
	word = strtok_r(NULL, " ", save);
	if (!parse_u32(strtok_r(NULL, " ", save), &ms) ||
	    !parse_transition(word, ms, &transition)) {
		reply_error(seq, "place takes none, fade or move and a duration");
		return;
	}

	s = surface_with_id(id);
	if (!s) {
		reply_error(seq, "no such surface");
		return;
	}
	ol = connector ? output_layer_named(connector) : NULL;
	if (!ol) {
		reply_error(seq, "no such output");
		return;
	}

	/* A surface new to this output goes on top of it. The operator
	 * states the stacking order with a following order request. */
	if (s->placed != ol) {
		if (s->placed) {
			wl_list_remove(&s->stack_link);
			apply_stack(s->placed);
		}
		s->placed = ol;
		wl_list_insert(ol->stack.prev, &s->stack_link);
		apply_stack(ol);
	}

	ivi->surface_set_transition(s->ivisurf, transition, ms);
	ivi->surface_set_destination_rectangle(s->ivisurf, x, y, w, h);
	ivi->surface_set_visibility(s->ivisurf, true);
	reply_ok(seq);
}

static void
do_hide(uint32_t seq, char **save)
{
	uint32_t id, ms;
	const char *word;
	enum ivi_layout_transition_type transition;
	struct layout_surface *s;

	if (!parse_u32(strtok_r(NULL, " ", save), &id)) {
		reply_error(seq, "hide takes a surface id");
		return;
	}
	word = strtok_r(NULL, " ", save);
	/* A hide takes no move: the surface is leaving the screen, and
	 * there is no rectangle to glide it to. */
	if (!parse_u32(strtok_r(NULL, " ", save), &ms) ||
	    !parse_transition(word, ms, &transition) ||
	    transition == IVI_LAYOUT_TRANSITION_VIEW_DEST_RECT_ONLY) {
		reply_error(seq, "hide takes none or fade and a duration");
		return;
	}
	s = surface_with_id(id);
	if (!s) {
		reply_error(seq, "no such surface");
		return;
	}
	/* ivi-layout reads the transition type on the commit that changes
	 * the visibility, and runs ivi_layout_transition_visibility_off
	 * for a fade, so the type is set before the change. The client
	 * keeps drawing through the fade: the compositor animates a
	 * surface it still holds, and a client that exits takes its
	 * surface with it. */
	ivi->surface_set_transition(s->ivisurf, transition, ms);
	ivi->surface_set_visibility(s->ivisurf, false);
	reply_ok(seq);
}

/* The listed surfaces move to the top of the output in the order
 * given, so the surfaces the operator did not list keep their relative
 * order below them. Every id is checked before any surface moves,
 * because a half-applied order would leave a stack the operator did
 * not state. */
static void
do_order(uint32_t seq, char **save)
{
	const char *connector = strtok_r(NULL, " ", save);
	struct layout_surface *listed[ORDER_MAX];
	struct output_layer *ol;
	const char *token;
	uint32_t id;
	int count = 0;
	int i;

	ol = connector ? output_layer_named(connector) : NULL;
	if (!ol) {
		reply_error(seq, "no such output");
		return;
	}

	while ((token = strtok_r(NULL, " ", save))) {
		if (count == ORDER_MAX) {
			send_line("error %u order names more than %d surfaces\n",
				  seq, ORDER_MAX);
			return;
		}
		if (!parse_u32(token, &id)) {
			reply_error(seq, "order takes surface ids");
			return;
		}
		listed[count] = surface_with_id(id);
		if (!listed[count]) {
			reply_error(seq, "no such surface");
			return;
		}
		if (listed[count]->placed != ol) {
			reply_error(seq, "that surface is not on that output");
			return;
		}
		count++;
	}

	for (i = 0; i < count; i++) {
		wl_list_remove(&listed[i]->stack_link);
		wl_list_insert(ol->stack.prev, &listed[i]->stack_link);
	}
	apply_stack(ol);
	reply_ok(seq);
}

/* Requests */

/* A new connection replays: the operator states every socket and every
 * placement again, so it needs the outputs and the surfaces the
 * compositor holds now. */
static void
send_state(void)
{
	struct output_layer *ol;
	struct layout_surface *s;

	wl_list_for_each(ol, &output_layers, link)
		send_output(ol->output);
	wl_list_for_each(s, &layout_surfaces, link)
		send_surface(s);
}

static void
do_hello(uint32_t seq, char **save)
{
	const char *who = strtok_r(NULL, " ", save);
	const char *version = strtok_r(NULL, " ", save);

	if (!who || !version || strcmp(version, "1") != 0) {
		reply_error(seq, "this module speaks version 1");
		return;
	}
	greeted = true;
	reply_ok(seq);
	send_state();
}

static void
handle_line(char *line)
{
	char *save = NULL;
	const char *seq_token = strtok_r(line, " ", &save);
	const char *verb = strtok_r(NULL, " ", &save);
	uint32_t seq;

	/* The sequence number is the first token, so a line that does
	 * not start with one cannot be answered by its number. Sequence
	 * zero tells the operator that its stream is out of step. */
	if (!parse_u32(seq_token, &seq)) {
		send_line("error 0 the first token is not a sequence number\n");
		return;
	}
	if (!verb) {
		reply_error(seq, "no request word after the sequence number");
		return;
	}

	if (strcmp(verb, "hello") == 0)
		do_hello(seq, &save);
	else if (!greeted)
		reply_error(seq, "send hello before any other request");
	else if (strcmp(verb, "listen") == 0)
		do_listen(seq, &save);
	else if (strcmp(verb, "close") == 0)
		do_close(seq, &save);
	else if (strcmp(verb, "place") == 0)
		do_place(seq, &save);
	else if (strcmp(verb, "hide") == 0)
		do_hide(seq, &save);
	else if (strcmp(verb, "order") == 0)
		do_order(seq, &save);
	else if (strcmp(verb, "commit") == 0) {
		ivi->commit_changes();
		reply_ok(seq);
	} else
		reply_error(seq, "unknown request word");
}

static int
on_control_readable(int fd, uint32_t mask, void *data)
{
	ssize_t count;
	char *line, *end;

	(void)mask;
	(void)data;
	count = read(fd, control_in + control_in_used,
		     sizeof control_in - control_in_used);
	if (count < 0) {
		if (errno == EAGAIN || errno == EINTR)
			return 0;
		weston_log("liken-layout: the operator's connection failed: %s\n",
			   strerror(errno));
		drop_connection();
		return 0;
	}
	if (count == 0) {
		weston_log("liken-layout: the operator disconnected. The last committed layout stays\n");
		drop_connection();
		return 0;
	}
	control_in_used += count;

	line = control_in;
	while ((end = memchr(line, '\n',
			     control_in_used - (size_t)(line - control_in)))) {
		*end = '\0';
		handle_line(line);
		if (control_fd < 0)
			return 0;
		line = end + 1;
	}

	control_in_used -= (size_t)(line - control_in);
	memmove(control_in, line, control_in_used);

	/* A full buffer with no newline in it is a request the module
	 * cannot parse, and reading more would never finish the line. */
	if (control_in_used == sizeof control_in) {
		weston_log("liken-layout: a request line is longer than %zu bytes\n",
			   sizeof control_in);
		drop_connection();
	}
	return 0;
}

static int
on_control_connect(int fd, uint32_t mask, void *data)
{
	int accepted;

	(void)mask;
	(void)data;
	accepted = accept4(fd, NULL, NULL, SOCK_CLOEXEC | SOCK_NONBLOCK);
	if (accepted < 0)
		return 0;

	/* One operator at a time. The connection that arrives last is
	 * the one that will replay the layout, so it replaces the old
	 * one. */
	if (control_fd >= 0) {
		weston_log("liken-layout: a second operator connected, so the module dropped the first\n");
		drop_connection();
	}
	control_fd = accepted;
	control_source = wl_event_loop_add_fd(wl_display_get_event_loop(compositor->wl_display),
					      control_fd, WL_EVENT_READABLE,
					      on_control_readable, NULL);
	send_line("hello liken-layout 1\n");
	return 0;
}

static const char *
control_path(void)
{
	const char *path = getenv("LIKEN_LAYOUT_SOCKET");

	return path && path[0] ? path : DEFAULT_CONTROL_PATH;
}

static int
start_control_socket(void)
{
	struct sockaddr_un addr = { .sun_family = AF_UNIX };
	const char *path = control_path();

	if (strlen(path) >= sizeof addr.sun_path) {
		weston_log("liken-layout: %s is too long for a Unix socket path\n", path);
		return -1;
	}
	snprintf(addr.sun_path, sizeof addr.sun_path, "%s", path);

	control_listen_fd = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC | SOCK_NONBLOCK, 0);
	if (control_listen_fd < 0) {
		weston_log("liken-layout: no control socket: %s\n", strerror(errno));
		return -1;
	}
	/* A compositor that stopped without unlinking leaves the node
	 * behind, and bind would fail on it. */
	unlink(path);
	if (bind(control_listen_fd, (struct sockaddr *)&addr, sizeof addr) < 0 ||
	    listen(control_listen_fd, 1) < 0) {
		weston_log("liken-layout: %s does not listen: %s\n", path, strerror(errno));
		close(control_listen_fd);
		control_listen_fd = -1;
		return -1;
	}

	if (!wl_event_loop_add_fd(wl_display_get_event_loop(compositor->wl_display),
				  control_listen_fd, WL_EVENT_READABLE,
				  on_control_connect, NULL)) {
		close(control_listen_fd);
		control_listen_fd = -1;
		return -1;
	}
	weston_log("liken-layout: the control socket is %s\n", path);
	return 0;
}

WL_EXPORT int
wet_module_init(struct weston_compositor *ec, int *argc, char *argv[])
{
	struct weston_output *output;

	(void)argc;
	(void)argv;

	ivi = ivi_layout_get_api(ec);
	if (!ivi) {
		weston_log("liken-layout: ivi-shell is not loaded, so there is no layout to control\n");
		return -1;
	}
	compositor = ec;

	weston_layer_init(&background_layer, ec);
	weston_layer_set_position(&background_layer,
				  WESTON_LAYER_POSITION_BACKGROUND);

	wl_list_init(&output_layers);
	wl_list_init(&listening_sockets);
	wl_list_init(&client_origins);
	wl_list_init(&layout_surfaces);

	wl_list_for_each(output, &ec->output_list, link)
		add_output_layer(output);

	configure_surface.notify = on_configure;
	ivi->add_listener_configure_surface(&configure_surface);
	configure_desktop_surface.notify = on_configure;
	ivi->add_listener_configure_desktop_surface(&configure_desktop_surface);
	remove_surface.notify = on_remove;
	ivi->add_listener_remove_surface(&remove_surface);

	client_created.notify = on_client_created;
	wl_display_add_client_created_listener(ec->wl_display, &client_created);

	output_created.notify = on_output_created;
	wl_signal_add(&ec->output_created_signal, &output_created);
	output_destroyed.notify = on_output_destroyed;
	wl_signal_add(&ec->output_destroyed_signal, &output_destroyed);
	output_resized.notify = on_output_resized;
	wl_signal_add(&ec->output_resized_signal, &output_resized);

	return start_control_socket();
}
