/*
 * liken_arrangement_v1: one client of a claim arranges the claim's
 * surfaces inside the claim's region. The XML beside this file is the
 * contract, and this file is the compositor's side of it.
 *
 * The operator stays the one writer of the layout. An arrangement never
 * changes a surface's region, never moves a surface outside it, and
 * never scales one above the region's size. It changes where each of
 * the claim's surfaces shows inside the region, how opaque it is, and
 * the order of the claim's surfaces inside the claim's own places in
 * the output's stack. So the authority is the claim: a client that
 * arrived on a claim's socket may arrange that claim, and the first one
 * that asks does.
 *
 * The state of a claim is a list of entries and a clock. Each surface
 * keeps where it showed at the last commit, and the engine in motion.c
 * asks arrangement_place where the surface shows at each frame: a
 * point on the curve from there to its entry, or to the default, which
 * fills the region. Nothing that moves keeps a value from one frame to
 * the next, so a commit in the middle of a tween starts from where the
 * surface shows in that frame.
 */
#define _GNU_SOURCE

#include <ctype.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

#include <libweston/desktop.h>
#include <libweston/libweston.h>

#include "liken-layout.h"
#include "liken-arrangement-v1-server-protocol.h"

/* A tween is a short motion. A longer duration or delay is a client
 * error, not a request the module runs for minutes. */
#define ARRANGE_MAX_MS 10000

/* When the arranger goes, the claim's surfaces go back to the default
 * over this long, so a crash of the arranger reads as a motion and not
 * as a jump. */
#define RELEASE_MS 400

/* The longest name of an arrangement that the module reports. */
#define ARRANGEMENT_NAME_LEN 128

struct entry {
	char app_id[APP_ID_LEN];
	struct rectf rect;
	double opacity;
	uint32_t fit;
	uint32_t delay;
};

struct entries {
	struct entry *items;
	int count;
};

struct app_id_count {
	char app_id[APP_ID_LEN];
	uint32_t count;
};

/* The arrangement state of one claim. It exists while a client arranges
 * the claim, and after that until the tween back to the default lands. */
struct claim {
	char socket[NAME_LEN];
	struct arranger *arranger;	/* NULL while the claim goes back */
	/* The clock runs the tween back to the default, whose end no
	 * arranger hears about, not even one granted while it runs. */
	bool releasing;

	struct entries committed;
	struct entries pending;

	/* The clock of the last commit. Its length is the longest delay
	 * plus the duration, so it runs until the last entry lands. */
	struct motion clock;
	uint32_t duration;
	uint32_t curve;
	uint32_t serial;
	char name[ARRANGEMENT_NAME_LEN];
	bool named;
	struct output_layer *ol;

	/* What the arranger was last told. */
	int32_t region_w, region_h;
	struct app_id_count *counts;
	int ncounts;

	struct wl_list link;
};

struct arranger {
	struct wl_resource *resource;
	struct wl_client *client;
	struct claim *claim;	/* NULL when refused or after release */
	/* The client asked to destroy the arranger, as opposed to going
	 * away with the arranger still in place. */
	bool released;
};

static struct wl_list claims;
static struct wl_event_source *drop_source;

/* Small helpers */

static double
ms_between(const struct timespec *a, const struct timespec *b)
{
	return (double)(b->tv_sec - a->tv_sec) * 1000.0 +
	       (double)(b->tv_nsec - a->tv_nsec) / 1e6;
}

static double
ease(uint32_t curve, double p)
{
	switch (curve) {
	case LIKEN_ARRANGER_V1_CURVE_EASE_OUT:
		return 1.0 - (1.0 - p) * (1.0 - p) * (1.0 - p);
	case LIKEN_ARRANGER_V1_CURVE_EASE_IN:
		return p * p * p;
	case LIKEN_ARRANGER_V1_CURVE_LINEAR:
		return p;
	default:
		return p * p * (3.0 - 2.0 * p);
	}
}

/* The control protocol splits a line at spaces, so a name or an app_id
 * goes out with every space and control character as an underscore. */
void
control_word(char *out, size_t len, const char *in)
{
	size_t i;

	for (i = 0; in && in[i] && i + 1 < len; i++)
		out[i] = isgraph((unsigned char)in[i]) ? in[i] : '_';
	out[i] = '\0';
	if (i == 0 && len > 1)
		snprintf(out, len, "-");
}

static struct claim *
claim_named(const char *socket)
{
	struct claim *c;

	wl_list_for_each(c, &claims, link)
		if (strcmp(c->socket, socket) == 0)
			return c;
	return NULL;
}

static const struct entry *
entry_for(const struct entries *list, const char *app_id)
{
	int i;

	if (!app_id[0])
		return NULL;
	for (i = 0; i < list->count; i++)
		if (strcmp(list->items[i].app_id, app_id) == 0)
			return &list->items[i];
	return NULL;
}

static int
entry_index(const struct entries *list, const char *app_id)
{
	const struct entry *e = entry_for(list, app_id);

	return e ? (int)(e - list->items) : -1;
}

static void
free_entries(struct entries *list)
{
	free(list->items);
	list->items = NULL;
	list->count = 0;
}

static struct rectf
full_region(struct layout_surface *s)
{
	const struct ivi_layout_surface_properties *prop =
		ivi->get_properties_of_surface(s->ivisurf);

	return (struct rectf){ 0, 0, prop->dest_width, prop->dest_height };
}

/* A surface whose own buffer is one colour: a single-pixel buffer, or a
 * one-pixel buffer that a viewport stretches. */
static bool
content_is_solid(struct layout_surface *s)
{
	struct weston_surface *ws = ivi->surface_get_weston_surface(s->ivisurf);
	struct weston_buffer *buffer = ws ? ws->buffer_ref.buffer : NULL;

	return buffer && (buffer->type == WESTON_BUFFER_SOLID ||
			  (buffer->width == 1 && buffer->height == 1));
}

static bool
has_subsurfaces(struct layout_surface *s)
{
	struct weston_view *view = top_view_of(s);

	return view && !wl_list_empty(&view->geometry.child_list);
}

/* The rectangle stays inside the region and never above its size, also
 * when the region shrinks under an arrangement that the arranger has
 * not committed again. */
static struct rectf
clamp_to(struct rectf r, struct rectf full)
{
	if (r.w > full.w)
		r.w = full.w;
	if (r.h > full.h)
		r.h = full.h;
	if (r.x < 0)
		r.x = 0;
	if (r.y < 0)
		r.y = 0;
	if (r.x + r.w > full.w)
		r.x = full.w - r.w;
	if (r.y + r.h > full.h)
		r.y = full.h - r.h;
	return r;
}

/* How far one surface is along the claim's tween at one frame, before
 * the curve. A surface waits its entry's delay, then moves for the
 * arrangement's duration. */
static double
surface_progress(struct claim *c, struct layout_surface *s,
		 const struct timespec *now)
{
	double t;

	if (!c->clock.running)
		return 1.0;
	if (!c->clock.started || !now)
		return 0.0;
	t = ms_between(&c->clock.start, now) - s->arr_delay;
	if (t < 0)
		return 0.0;
	if (c->duration == 0 || t >= c->duration)
		return 1.0;
	return t / c->duration;
}

/* weston-desktop sends the configure. The surface keeps its region, so
 * ivi-layout scales the buffer of the new size to the region, and the
 * engine's transform scales the region to the rectangle: the two cancel,
 * and the client's pixels land one to one once it draws at that size. */
static void
send_configure(struct layout_surface *s, int32_t w, int32_t h)
{
	struct weston_surface *ws = ivi->surface_get_weston_surface(s->ivisurf);
	struct weston_desktop_surface *dsurface =
		ws ? weston_surface_get_desktop_surface(ws) : NULL;

	if (!dsurface)
		return;
	weston_desktop_surface_set_size(dsurface, w, h);
	weston_log("liken-layout: the arrangement of %s resized surface %u to %dx%d\n",
		   s->socket_name, s->id, w, h);
}

void
arrangement_place(struct layout_surface *s, const struct timespec *now,
		  struct arranged *out)
{
	struct claim *c = claim_named(s->socket_name);
	struct rectf full = full_region(s), to = full;
	const struct entry *e = NULL;
	double opacity = 1.0, p;
	uint32_t fit = LIKEN_ARRANGER_V1_FIT_SCALE;

	out->rect = full;
	out->opacity = 1.0;
	out->split = false;
	if (!c)
		return;

	e = entry_for(&c->committed, s->app_id);
	if (e) {
		to = e->rect;
		opacity = e->opacity;
		fit = e->fit;
	}
	p = surface_progress(c, s, now);
	if (s->arr_from_valid) {
		double k = ease(c->curve, p);

		out->rect = (struct rectf){
			s->arr_from.x + (to.x - s->arr_from.x) * k,
			s->arr_from.y + (to.y - s->arr_from.y) * k,
			s->arr_from.w + (to.w - s->arr_from.w) * k,
			s->arr_from.h + (to.h - s->arr_from.h) * k,
		};
		out->opacity = s->arr_alpha_from + (opacity - s->arr_alpha_from) * k;
	} else {
		out->rect = to;
		out->opacity = opacity;
	}
	out->rect = clamp_to(out->rect, full);
	out->split = fit == LIKEN_ARRANGER_V1_FIT_SCALE && content_is_solid(s) &&
		     has_subsurfaces(s);

	/* A resize lands with one configure. */
	if (p >= 1.0 && s->arr_configure_w > 0) {
		send_configure(s, s->arr_configure_w, s->arr_configure_h);
		s->arr_configured_w = s->arr_configure_w;
		s->arr_configured_h = s->arr_configure_h;
		s->arr_configure_w = s->arr_configure_h = 0;
		if (!e) {
			/* Back at the region's size, which ivi-layout would
			 * have configured anyway. */
			s->arr_configured_w = s->arr_configured_h = 0;
		}
	}
}

/* Stacking */

static int
compare_stack(const void *a, const void *b, void *data)
{
	const struct entries *list = data;
	const struct layout_surface *x = *(struct layout_surface *const *)a;
	const struct layout_surface *y = *(struct layout_surface *const *)b;
	int ix = entry_index(list, x->app_id), iy = entry_index(list, y->app_id);

	if (ix != iy)
		return ix < iy ? -1 : 1;
	return x->id < y->id ? -1 : x->id > y->id;
}

/* The claim's surfaces keep the places in the stack that the operator
 * gave them, so they never move above or below another claim's
 * surfaces. Inside those places, the surfaces that no entry names go
 * first in the order they arrived, and the named ones follow in the
 * order of their entries. A claim with no entries keeps the operator's
 * order. */
void
arrangement_order(struct layout_surface **stack, int count)
{
	struct layout_surface **mine;
	struct claim *c;
	int *places;
	int i, n;

	mine = calloc(count + 1, sizeof *mine);
	places = calloc(count + 1, sizeof *places);
	if (!mine || !places)
		goto out;
	wl_list_for_each(c, &claims, link) {
		if (c->committed.count == 0)
			continue;
		n = 0;
		for (i = 0; i < count; i++)
			if (strcmp(stack[i]->socket_name, c->socket) == 0) {
				places[n] = i;
				mine[n++] = stack[i];
			}
		qsort_r(mine, n, sizeof *mine, compare_stack, &c->committed);
		for (i = 0; i < n; i++)
			stack[places[i]] = mine[i];
	}
out:
	free(mine);
	free(places);
}

static void
restack_claim(struct claim *c)
{
	struct output_layer *ol;
	struct layout_surface *s;

	wl_list_for_each(ol, &output_layers, link)
		wl_list_for_each(s, &ol->stack, stack_link)
			if (strcmp(s->socket_name, c->socket) == 0) {
				apply_stack(ol);
				break;
			}
}

/* Events to the arranger */

static struct layout_surface *
first_placed(const char *socket)
{
	struct layout_surface *s, *first = NULL;

	wl_list_for_each(s, &layout_surfaces, link)
		if (s->placed && strcmp(s->socket_name, socket) == 0 &&
		    (!first || s->id < first->id))
			first = s;
	return first;
}

static void
send_region(struct claim *c, bool always)
{
	struct layout_surface *s = first_placed(c->socket);
	struct rectf full = s ? full_region(s) : (struct rectf){ 0, 0, 0, 0 };

	if (!c->arranger)
		return;
	if (!always && c->region_w == (int32_t)full.w && c->region_h == (int32_t)full.h)
		return;
	c->region_w = full.w;
	c->region_h = full.h;
	liken_arranger_v1_send_region(c->arranger->resource, c->region_w, c->region_h);
}

static void
send_counts(struct claim *c)
{
	struct app_id_count *now = NULL;
	struct layout_surface *s;
	int n = 0, i, j;
	bool found;

	if (!c->arranger)
		return;
	wl_list_for_each(s, &layout_surfaces, link) {
		if (!s->app_id[0] || strcmp(s->socket_name, c->socket) != 0)
			continue;
		for (i = 0; i < n; i++)
			if (strcmp(now[i].app_id, s->app_id) == 0)
				break;
		if (i == n) {
			struct app_id_count *grown = realloc(now, (n + 1) * sizeof *now);

			if (!grown)
				continue;
			now = grown;
			snprintf(now[n].app_id, sizeof now[n].app_id, "%s", s->app_id);
			now[n++].count = 0;
		}
		now[i].count++;
	}
	for (i = 0; i < n; i++) {
		found = false;
		for (j = 0; j < c->ncounts; j++)
			if (strcmp(c->counts[j].app_id, now[i].app_id) == 0) {
				found = c->counts[j].count == now[i].count;
				break;
			}
		if (!found)
			liken_arranger_v1_send_surfaces(c->arranger->resource,
							now[i].app_id, now[i].count);
	}
	for (j = 0; j < c->ncounts; j++) {
		found = false;
		for (i = 0; i < n; i++)
			found |= strcmp(c->counts[j].app_id, now[i].app_id) == 0;
		if (!found)
			liken_arranger_v1_send_surfaces(c->arranger->resource,
							c->counts[j].app_id, 0);
	}
	free(c->counts);
	c->counts = now;
	c->ncounts = n;
}

void
arrangement_layout_changed(void)
{
	struct claim *c;

	wl_list_for_each(c, &claims, link)
		send_region(c, false);
}

void
arrangement_surfaces_changed(const char *socket_name)
{
	struct claim *c = claim_named(socket_name);

	if (c)
		send_counts(c);
}

void
arrangement_send_state(void)
{
	struct claim *c;
	char name[ARRANGEMENT_NAME_LEN];
	pid_t pid;

	wl_list_for_each(c, &claims, link) {
		if (!c->arranger)
			continue;
		wl_client_get_credentials(c->arranger->client, &pid, NULL, NULL);
		send_line("arranger %s %d\n", c->socket, (int)pid);
		if (c->named) {
			control_word(name, sizeof name, c->name);
			send_line("arrangement %s %u %s\n", c->socket, c->serial, name);
		}
	}
}

/* The clock */

void
arrangement_frame_begin(struct output_layer *ol, const struct timespec *now)
{
	struct claim *c;

	wl_list_for_each(c, &claims, link)
		if (c->ol == ol && c->clock.running && !c->clock.started) {
			c->clock.start = *now;
			c->clock.started = true;
		}
}

bool
arrangement_output_busy(struct output_layer *ol)
{
	struct claim *c;

	wl_list_for_each(c, &claims, link)
		if (c->ol == ol && c->clock.running)
			return true;
	return false;
}

static void
free_claim(struct claim *c)
{
	wl_list_remove(&c->link);
	free_entries(&c->committed);
	free_entries(&c->pending);
	free(c->counts);
	free(c);
}

/* A claim whose arranger is gone and whose surfaces are back at the
 * default goes, and its surfaces go back to the operator's stacking
 * order. The stack changes with a commit, which waits for the event
 * loop's next idle point, because a commit inside weston's repaint
 * would rebuild ivi-layout's layers while the repaint runs. */
static void
drop_released(void *data)
{
	struct claim *c, *next;
	struct layout_surface *s;
	bool any = false;

	(void)data;
	drop_source = NULL;
	wl_list_for_each_safe(c, next, &claims, link) {
		if (c->arranger || c->clock.running)
			continue;
		wl_list_for_each(s, &layout_surfaces, link)
			if (strcmp(s->socket_name, c->socket) == 0)
				s->arr_from_valid = false;
		restack_claim(c);
		free_claim(c);
		any = true;
	}
	if (any)
		layout_commit();
}

static void
schedule_drop(void)
{
	if (!drop_source)
		drop_source = wl_event_loop_add_idle(
			wl_display_get_event_loop(compositor->wl_display),
			drop_released, NULL);
}

static void
landed(struct claim *c)
{
	c->clock.running = false;
	if (c->arranger && !c->releasing)
		liken_arranger_v1_send_landed(c->arranger->resource, c->serial);
	if (!c->arranger)
		schedule_drop();
}

void
arrangement_frame_done(struct output_layer *ol, const struct timespec *now)
{
	struct claim *c;

	wl_list_for_each(c, &claims, link)
		if (c->ol == ol && c->clock.running && c->clock.started &&
		    ms_between(&c->clock.start, now) >= (double)c->clock.ms)
			landed(c);
}

void
arrangement_output_dropped(struct output_layer *ol)
{
	struct claim *c;

	wl_list_for_each(c, &claims, link)
		if (c->ol == ol) {
			c->ol = NULL;
			if (c->clock.running)
				landed(c);
		}
}

/* Applies a list of entries to a claim: every surface of the claim
 * starts from where it shows in the last frame, and the clock starts
 * at the next one. */
static void
apply(struct claim *c, struct entries *list, uint32_t duration, uint32_t curve)
{
	struct layout_surface *s, *first;
	const struct entry *e;
	struct arranged now;
	const char *connector;
	uint32_t longest = 0;
	int i;

	wl_list_for_each(s, &layout_surfaces, link) {
		if (strcmp(s->socket_name, c->socket) != 0)
			continue;
		arrangement_place(s, s->placed ? &s->placed->output->frame_time : NULL, &now);
		s->arr_from = now.rect;
		s->arr_alpha_from = now.opacity;
		s->arr_from_valid = s->placed != NULL;
	}

	free_entries(&c->committed);
	c->committed = *list;
	*list = (struct entries){ NULL, 0 };
	for (i = 0; i < c->committed.count; i++)
		if (c->committed.items[i].delay > longest)
			longest = c->committed.items[i].delay;

	wl_list_for_each(s, &layout_surfaces, link) {
		if (strcmp(s->socket_name, c->socket) != 0)
			continue;
		e = entry_for(&c->committed, s->app_id);
		s->arr_delay = e ? e->delay : 0;
		s->arr_configure_w = s->arr_configure_h = 0;
		if (e && e->fit == LIKEN_ARRANGER_V1_FIT_RESIZE &&
		    (s->arr_configured_w != (int32_t)e->rect.w ||
		     s->arr_configured_h != (int32_t)e->rect.h)) {
			s->arr_configure_w = e->rect.w;
			s->arr_configure_h = e->rect.h;
		} else if (!e && s->arr_configured_w > 0) {
			s->arr_configure_w = full_region(s).w;
			s->arr_configure_h = full_region(s).h;
		}
	}

	first = first_placed(c->socket);
	connector = connector_of_socket(c->socket);
	c->ol = first ? first->placed : connector ? output_layer_named(connector) : NULL;
	c->duration = duration;
	c->curve = curve;
	motion_begin(&c->clock, c->ol ? longest + duration : 0);

	restack_claim(c);
	layout_commit();
	if (!c->clock.running)
		landed(c);
}

/* Requests */

static struct claim *
granted_claim(struct arranger *a)
{
	if (a->claim)
		return a->claim;
	wl_resource_post_error(a->resource, LIKEN_ARRANGER_V1_ERROR_NOT_GRANTED,
			       "this arranger was not granted the claim");
	return NULL;
}

static void
arranger_entry(struct wl_client *client, struct wl_resource *resource,
	       const char *app_id, int32_t x, int32_t y, int32_t width,
	       int32_t height, wl_fixed_t opacity, uint32_t fit, uint32_t delay_ms)
{
	struct arranger *a = wl_resource_get_user_data(resource);
	struct claim *c = granted_claim(a);
	struct entry *grown, *e;
	double alpha = wl_fixed_to_double(opacity);

	(void)client;
	if (!c)
		return;
	if (width <= 0 || height <= 0 || x < 0 || y < 0 ||
	    (c->region_w > 0 && (int64_t)x + width > c->region_w) ||
	    (c->region_h > 0 && (int64_t)y + height > c->region_h)) {
		wl_resource_post_error(resource, LIKEN_ARRANGER_V1_ERROR_INVALID_RECT,
				       "%d,%d %dx%d is not inside the region %dx%d",
				       x, y, width, height, c->region_w, c->region_h);
		return;
	}
	if (alpha < 0.0 || alpha > 1.0) {
		wl_resource_post_error(resource, LIKEN_ARRANGER_V1_ERROR_INVALID_OPACITY,
				       "an opacity is between 0 and 1");
		return;
	}
	if (delay_ms > ARRANGE_MAX_MS) {
		wl_resource_post_error(resource, LIKEN_ARRANGER_V1_ERROR_INVALID_DURATION,
				       "a delay is at most %d ms", ARRANGE_MAX_MS);
		return;
	}
	if (fit != LIKEN_ARRANGER_V1_FIT_SCALE && fit != LIKEN_ARRANGER_V1_FIT_RESIZE) {
		wl_resource_post_error(resource, LIKEN_ARRANGER_V1_ERROR_INVALID_ENUM,
				       "fit %u is neither scale nor resize", fit);
		return;
	}
	if (entry_for(&c->pending, app_id) || strlen(app_id) >= APP_ID_LEN) {
		wl_resource_post_error(resource, LIKEN_ARRANGER_V1_ERROR_DUPLICATE_ENTRY,
				       "the arrangement names %s twice, or the app_id is too long",
				       app_id);
		return;
	}
	grown = realloc(c->pending.items, (c->pending.count + 1) * sizeof *grown);
	if (!grown) {
		wl_client_post_no_memory(client);
		return;
	}
	c->pending.items = grown;
	e = &grown[c->pending.count++];
	snprintf(e->app_id, sizeof e->app_id, "%s", app_id);
	e->rect = (struct rectf){ x, y, width, height };
	e->opacity = alpha;
	e->fit = fit;
	e->delay = delay_ms;
}

static void
arranger_commit(struct wl_client *client, struct wl_resource *resource,
		uint32_t serial, const char *name, uint32_t duration_ms, uint32_t curve)
{
	struct arranger *a = wl_resource_get_user_data(resource);
	struct claim *c = granted_claim(a);
	char word[ARRANGEMENT_NAME_LEN];

	(void)client;
	if (!c)
		return;
	if (duration_ms > ARRANGE_MAX_MS) {
		wl_resource_post_error(resource, LIKEN_ARRANGER_V1_ERROR_INVALID_DURATION,
				       "a duration is at most %d ms", ARRANGE_MAX_MS);
		return;
	}
	if (curve > LIKEN_ARRANGER_V1_CURVE_LINEAR) {
		wl_resource_post_error(resource, LIKEN_ARRANGER_V1_ERROR_INVALID_ENUM,
				       "curve %u is not a curve", curve);
		return;
	}
	c->serial = serial;
	c->releasing = false;
	snprintf(c->name, sizeof c->name, "%s", name);
	c->named = true;
	control_word(word, sizeof word, name);
	send_line("arrangement %s %u %s\n", c->socket, serial, word);
	weston_log("liken-layout: %s commits arrangement %u %s over %u ms\n",
		   c->socket, serial, word, duration_ms);
	apply(c, &c->pending, duration_ms, curve);
}

static void
arranger_destroy_request(struct wl_client *client, struct wl_resource *resource)
{
	struct arranger *a = wl_resource_get_user_data(resource);

	(void)client;
	a->released = true;
	wl_resource_destroy(resource);
}

static const struct liken_arranger_v1_interface arranger_impl = {
	.destroy = arranger_destroy_request,
	.entry = arranger_entry,
	.commit = arranger_commit,
};

/* The arranger's owner is its resource, so a client that destroys it
 * or disconnects gives the claim back. Every surface of the claim
 * tweens to the default, and the claim takes the next client that asks. */
static void
arranger_resource_destroyed(struct wl_resource *resource)
{
	struct arranger *a = wl_resource_get_user_data(resource);
	struct claim *c = a->claim;
	struct entries none = { NULL, 0 };

	if (c) {
		c->arranger = NULL;
		c->releasing = true;
		c->named = false;
		free_entries(&c->pending);
		free(c->counts);
		c->counts = NULL;
		c->ncounts = 0;
		send_line("arranger-gone %s %s\n", c->socket,
			  a->released ? "released" : "disconnected");
		weston_log("liken-layout: the arranger of %s is gone, %s\n", c->socket,
			   a->released ? "released" : "disconnected");
		apply(c, &none, RELEASE_MS, LIKEN_ARRANGER_V1_CURVE_SMOOTHSTEP);
	}
	free(a);
}

/* A client on wayland-0 or the capture socket, or a second client of a
 * claim, gets an arranger that sends refused, not a protocol error. An
 * error would disconnect the client, and its windows would go with it. */
static void
manager_get_arranger(struct wl_client *client, struct wl_resource *manager,
		     uint32_t id)
{
	const char *origin = origin_of_client(client);
	struct arranger *a = calloc(1, sizeof *a);
	struct claim *c;
	pid_t pid;

	if (!a) {
		wl_client_post_no_memory(client);
		return;
	}
	a->resource = wl_resource_create(client, &liken_arranger_v1_interface,
					 wl_resource_get_version(manager), id);
	if (!a->resource) {
		free(a);
		wl_client_post_no_memory(client);
		return;
	}
	a->client = client;
	wl_resource_set_implementation(a->resource, &arranger_impl, a,
				       arranger_resource_destroyed);

	if (strcmp(origin, SHARED_SOCKET) == 0 || strcmp(origin, CAPTURE_SOCKET) == 0) {
		liken_arranger_v1_send_refused(a->resource, LIKEN_ARRANGER_V1_REFUSAL_NOT_A_CLAIM);
		weston_log("liken-layout: a client on %s asked to arrange, refused\n", origin);
		return;
	}
	c = claim_named(origin);
	if (c && c->arranger) {
		liken_arranger_v1_send_refused(a->resource, LIKEN_ARRANGER_V1_REFUSAL_BUSY);
		weston_log("liken-layout: a second client of %s asked to arrange, refused\n", origin);
		return;
	}
	if (!c) {
		c = calloc(1, sizeof *c);
		if (!c) {
			wl_client_post_no_memory(client);
			return;
		}
		snprintf(c->socket, sizeof c->socket, "%s", origin);
		c->curve = LIKEN_ARRANGER_V1_CURVE_SMOOTHSTEP;
		wl_list_insert(claims.prev, &c->link);
	}
	c->arranger = a;
	a->claim = c;

	wl_client_get_credentials(client, &pid, NULL, NULL);
	send_line("arranger %s %d\n", c->socket, (int)pid);
	weston_log("liken-layout: pid %d arranges %s\n", (int)pid, c->socket);
	liken_arranger_v1_send_granted(a->resource);
	send_region(c, true);
	send_counts(c);
}

static void
manager_destroy(struct wl_client *client, struct wl_resource *resource)
{
	(void)client;
	wl_resource_destroy(resource);
}

static const struct liken_arranger_manager_v1_interface manager_impl = {
	.destroy = manager_destroy,
	.get_arranger = manager_get_arranger,
};

static void
bind_manager(struct wl_client *client, void *data, uint32_t version, uint32_t id)
{
	struct wl_resource *resource;

	(void)data;
	resource = wl_resource_create(client, &liken_arranger_manager_v1_interface,
				      version, id);
	if (!resource) {
		wl_client_post_no_memory(client);
		return;
	}
	wl_resource_set_implementation(resource, &manager_impl, NULL, NULL);
}

/* The global is advertised to every client, as weston_capture_v1 is,
 * and the authority is checked when a client asks to arrange. A global
 * filter would do the same, but libwayland allows one filter per
 * display, and weston may install its own. */
bool
arrangement_init(void)
{
	wl_list_init(&claims);
	return wl_global_create(compositor->wl_display,
				&liken_arranger_manager_v1_interface, 1,
				NULL, bind_manager) != NULL;
}
