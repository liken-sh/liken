/*
 * The animation engine of liken-layout.so. Every motion on a liken
 * screen that the compositor runs goes through it: the fade and the
 * move of a place or a hide, and the tweens of a claim's arrangement.
 *
 * ivi-layout runs its own transitions on a 30 frames a second timer
 * with a sine ease, whatever the panel's refresh rate is. This engine
 * runs instead on each repaint of the output. weston calls every
 * weston_animation on output->animation_list after it posts a frame,
 * with the time of the last page flip, so each step lands on a vsync,
 * and a missed vblank skips a step rather than slowing the motion. The
 * curve is smoothstep, the one the media browser uses.
 *
 * Each motion is a pure function of that clock: a start, a length, and
 * a curve. The engine keeps no value from one frame to the next, so a
 * frame that weston skips changes nothing that follows it.
 *
 * The module never hands a transition to ivi-layout. A fade sets the
 * alpha of the surface's view on each frame. A move commits the new
 * region at once, so the client gets one configure at the start, and
 * then a weston_transform scales the view from the old region to the
 * new one. An arrangement uses the same transform. The transform
 * node goes at the tail of the view's transformation list, because
 * weston applies the list head first and ivi-layout keeps its own node
 * at the head: the engine's matrix then acts on output coordinates,
 * after ivi-layout has put the surface in its region.
 */
#define _GNU_SOURCE

#include <math.h>
#include <stdlib.h>
#include <string.h>

#include <libweston/libweston.h>

#include "liken-layout.h"

static double
ms_between(const struct timespec *a, const struct timespec *b)
{
	return (double)(b->tv_sec - a->tv_sec) * 1000.0 +
	       (double)(b->tv_nsec - a->tv_nsec) / 1e6;
}

/* The eased progress of a motion at the time of one frame. A motion
 * that is not running has arrived, and one that no frame has started
 * yet has not left. */
double
motion_progress(const struct motion *m, const struct timespec *now)
{
	double p;

	if (!m->running)
		return 1.0;
	if (!m->started || !now)
		return 0.0;
	p = ms_between(&m->start, now) / (double)m->ms;
	if (p <= 0.0)
		return 0.0;
	if (p >= 1.0)
		return 1.0;
	return p * p * (3.0 - 2.0 * p);
}

/* A motion of zero milliseconds is not running at all, so it has
 * arrived before any frame. */
void
motion_begin(struct motion *m, uint32_t ms)
{
	m->running = ms > 0;
	m->started = false;
	m->ms = ms;
}

static void
motion_tick(struct motion *m, const struct timespec *now)
{
	if (m->running && !m->started) {
		m->start = *now;
		m->started = true;
	}
}

static bool
motion_done(const struct motion *m, const struct timespec *now)
{
	return m->running && m->started &&
	       ms_between(&m->start, now) >= (double)m->ms;
}

static double
lerp(double a, double b, double e)
{
	return a + (b - a) * e;
}

static struct rectf
lerp_rect(struct rectf a, struct rectf b, double e)
{
	return (struct rectf){
		lerp(a.x, b.x, e), lerp(a.y, b.y, e),
		lerp(a.w, b.w, e), lerp(a.h, b.h, e),
	};
}

static const struct timespec *
now_of(struct output_layer *ol)
{
	return ol ? &ol->output->frame_time : NULL;
}

/* The views */

/* The ivi export gives no view for a surface, so the engine walks the
 * surface's views. A surface that the operator moved to another output
 * keeps an unmapped view on the old one, and ivi-layout reuses that
 * view if the surface comes back, so the walk takes only a mapped
 * top-level view on the surface's own output. A view that weston has
 * not yet assigned to an output has a NULL output. */
struct weston_view *
top_view_of(struct layout_surface *s)
{
	struct weston_surface *ws = ivi->surface_get_weston_surface(s->ivisurf);
	struct weston_view *view;

	if (!ws || !s->placed)
		return NULL;
	wl_list_for_each(view, &ws->views, surface_link)
		if (!view->parent_view && view->layer_link.layer &&
		    (view->output == s->placed->output || !view->output))
			return view;
	return NULL;
}

/* weston frees a view with the node still in its list when the client
 * exits or the output goes, so the node leaves the list here, before
 * the free. The view is on its way out, so the engine touches only its
 * own node. */
static void
on_view_destroy(struct wl_listener *listener, void *data)
{
	struct layout_surface *s = wl_container_of(listener, s, view_destroy);

	(void)data;
	wl_list_remove(&s->node.link);
	wl_list_init(&s->node.link);
	wl_list_remove(&s->view_destroy.link);
	s->view = NULL;
}

/* Taking the node out, rather than leaving an identity in the list,
 * returns the view's matrix to a plain translation, which keeps
 * weston's exact opaque-region path. */
static void
detach(struct layout_surface *s)
{
	if (!s->view)
		return;
	weston_view_remove_transform(s->view, &s->node);
	wl_list_remove(&s->view_destroy.link);
	s->view = NULL;
}

/* The node goes in once and stays at the tail. weston_view_add_transform
 * unlinks the node before it inserts it, so adding a node that is
 * already the tail again would insert it after itself. */
static void
attach(struct layout_surface *s, struct weston_view *view)
{
	if (s->view == view)
		return;
	detach(s);
	wl_list_init(&s->node.link);
	weston_view_add_transform(view, view->geometry.transformation_list.prev,
				  &s->node);
	s->view = view;
	s->view_destroy.notify = on_view_destroy;
	wl_signal_add(&view->destroy_signal, &s->view_destroy);
}

/* The region and its motion */

static struct rectf
committed_region(struct layout_surface *s)
{
	const struct ivi_layout_surface_properties *prop =
		ivi->get_properties_of_surface(s->ivisurf);

	return (struct rectf){ prop->dest_x, prop->dest_y,
			       prop->dest_width, prop->dest_height };
}

/* Where the surface's region is on screen at one frame: the region
 * ivi-layout holds, or a point on a move toward it. */
struct rectf
region_of(struct layout_surface *s, const struct timespec *now)
{
	struct rectf to = committed_region(s);

	if (!s->move.running)
		return to;
	return lerp_rect(s->move_from, to, motion_progress(&s->move, now));
}

static double
alpha_of(struct layout_surface *s, const struct timespec *now)
{
	if (s->fade.running)
		return lerp(s->alpha_from, s->alpha_to,
			    motion_progress(&s->fade, now));
	return s->hiding ? 0.0 : 1.0;
}

/* Whole output pixels. A view whose edge falls inside a pixel gets an
 * opaque region up to one pixel larger than what the renderer draws,
 * so weston culls the curtain under a pixel that nobody paints, and
 * the edge keeps the previous frame's colour. */
static struct rectf
snap(struct rectf r, int32_t scale)
{
	double x0 = round(r.x * scale) / scale;
	double y0 = round(r.y * scale) / scale;
	double x1 = round((r.x + r.w) * scale) / scale;
	double y1 = round((r.y + r.h) * scale) / scale;

	return (struct rectf){ x0, y0, x1 - x0, y1 - y0 };
}

static bool
same_rect(struct rectf a, struct rectf b)
{
	return fabs(a.x - b.x) < 1e-6 && fabs(a.y - b.y) < 1e-6 &&
	       fabs(a.w - b.w) < 1e-6 && fabs(a.h - b.h) < 1e-6;
}

/* The arrangement's transform on one view of a subsurface. */
struct child_node {
	struct layout_surface *s;
	struct weston_view *view;
	struct weston_transform node;
	struct wl_listener view_destroy;
	struct wl_list link;
};

static void
drop_child_node(struct child_node *c, bool view_alive)
{
	if (view_alive) {
		weston_view_remove_transform(c->view, &c->node);
	} else {
		wl_list_remove(&c->node.link);
	}
	wl_list_remove(&c->view_destroy.link);
	wl_list_remove(&c->link);
	free(c);
}

static void
on_child_view_destroy(struct wl_listener *listener, void *data)
{
	struct child_node *c = wl_container_of(listener, c, view_destroy);

	(void)data;
	drop_child_node(c, false);
}

static void
drop_child_nodes(struct layout_surface *s)
{
	struct child_node *c, *next;

	wl_list_for_each_safe(c, next, &s->child_nodes, link)
		drop_child_node(c, true);
}

/* Puts the matrix m on every direct child view of the top-level view,
 * and takes the node off a view that is no longer one. A subsurface of
 * a subsurface takes its parent's transform, so mpv's OSD, a child of
 * the video subsurface, follows the video with no node of its own. */
static void
set_child_nodes(struct layout_surface *s, struct weston_view *view,
		const struct weston_matrix *m)
{
	struct child_node *c, *next;
	struct weston_view *child;
	bool found;

	wl_list_for_each_safe(c, next, &s->child_nodes, link) {
		found = false;
		wl_list_for_each(child, &view->geometry.child_list, geometry.parent_link)
			found |= child == c->view;
		if (!found)
			drop_child_node(c, true);
	}
	wl_list_for_each(child, &view->geometry.child_list, geometry.parent_link) {
		found = false;
		wl_list_for_each(c, &s->child_nodes, link) {
			if (c->view == child) {
				found = true;
				c->node.matrix = *m;
				weston_view_geometry_dirty(child);
			}
		}
		if (found)
			continue;
		c = calloc(1, sizeof *c);
		if (!c)
			continue;
		c->s = s;
		c->view = child;
		c->node.matrix = *m;
		wl_list_init(&c->node.link);
		weston_view_add_transform(child, child->geometry.transformation_list.prev,
					  &c->node);
		c->view_destroy.notify = on_child_view_destroy;
		wl_signal_add(&child->destroy_signal, &c->view_destroy);
		wl_list_insert(&s->child_nodes, &c->link);
	}
}

/* Sets the alpha of a view and of every subsurface view under it. weston
 * draws each view with its own alpha, so a fade of the top-level view
 * alone would leave a video subsurface opaque. */
static void
set_alpha_tree(struct weston_view *view, double alpha)
{
	struct weston_view *child;

	if (view->alpha != (float)alpha)
		weston_view_set_alpha(view, alpha);
	wl_list_for_each(child, &view->geometry.child_list, geometry.parent_link)
		set_alpha_tree(child, alpha);
}

/* Sets the view's transform and alpha for one frame. ivi-layout has put
 * the surface in its committed region F. The region shows at M, which
 * is F or a point on a move toward F, and the claim's arrangement puts
 * the surface at the rectangle A of the region, in the region's
 * logical pixels, so on screen at D. A rectangle keeps its pixels when
 * a move changes the region's size on screen, so A scales with M.
 *
 * A surface whose own content is one solid colour and which has
 * subsurfaces keeps its own view at M, and only the subsurfaces go to
 * D. mpv draws its window as a solid black backdrop that covers the
 * output, with the picture on a subsurface, and weston turns such a
 * view into the CRTC's background colour instead of a plane. When the
 * backdrop shrank, no solid view covered the output, weston 16.0.0
 * wrote a transparent background colour, and i915 refused every atomic
 * commit while the film was small, so the picture froze. Any other
 * surface moves as a whole. */
static void
refresh_surface(struct layout_surface *s, const struct timespec *now)
{
	const struct ivi_layout_surface_properties *prop;
	struct weston_output *output;
	struct weston_view *view = top_view_of(s);
	struct weston_matrix *m = &s->node.matrix, child;
	struct rectf f, region, d, own;
	struct arranged a;
	double gx, gy, sw, sh;

	if (!view)
		return;
	output = s->placed->output;
	prop = ivi->get_properties_of_surface(s->ivisurf);
	f = committed_region(s);
	region = snap(region_of(s, now), output->current_scale);
	arrangement_place(s, now, &a);
	if (f.w > 0 && f.h > 0)
		d = snap((struct rectf){ region.x + a.rect.x * region.w / f.w,
					 region.y + a.rect.y * region.h / f.h,
					 a.rect.w * region.w / f.w,
					 a.rect.h * region.h / f.h },
			 output->current_scale);
	else
		d = region;
	own = a.split ? region : d;

	if (f.w <= 0 || f.h <= 0 || same_rect(own, f)) {
		detach(s);
	} else {
		/* The matrix is set before the node goes in, because adding
		 * the node computes the view's transform at once. */
		gx = output->pos.c.x;
		gy = output->pos.c.y;
		weston_matrix_init(m);
		weston_matrix_translate(m, -(f.x + gx), -(f.y + gy), 0);
		weston_matrix_scale(m, own.w / f.w, own.h / f.h, 1);
		weston_matrix_translate(m, own.x + gx, own.y + gy, 0);
		attach(s, view);
		weston_view_geometry_dirty(view);
	}

	/* A child view's list acts in the parent surface's coordinates,
	 * before the parent's own transform, which puts the parent's
	 * surface at M. So the child's matrix moves the parent's surface
	 * rectangle onto the place in it that D takes. */
	if (!a.split || same_rect(d, region) || region.w <= 0 || region.h <= 0 ||
	    prop->source_width <= 0 || prop->source_height <= 0) {
		drop_child_nodes(s);
	} else {
		sw = prop->source_width;
		sh = prop->source_height;
		weston_matrix_init(&child);
		weston_matrix_scale(&child, d.w / region.w, d.h / region.h, 1);
		weston_matrix_translate(&child, (d.x - region.x) * sw / region.w,
					(d.y - region.y) * sh / region.h, 0);
		set_child_nodes(s, view, &child);
	}

	set_alpha_tree(view, alpha_of(s, now) * a.opacity);
}

/* A client adds a subsurface, sets its app_id, or changes its content
 * with a commit, so the module reads the app_id at each commit, and the
 * engine looks again at a surface the arrangement holds anywhere but
 * its region. A surface at its full size has nothing to do here. */
static void
on_commit(struct wl_listener *listener, void *data)
{
	struct layout_surface *s = wl_container_of(listener, s, commit);

	(void)data;
	surface_committed(s);
	if (s->view || !wl_list_empty(&s->child_nodes))
		refresh_surface(s, s->placed ? &s->placed->output->frame_time : NULL);
}

void
motion_surface_added(struct layout_surface *s)
{
	struct weston_surface *ws = ivi->surface_get_weston_surface(s->ivisurf);

	wl_list_init(&s->child_nodes);
	s->commit.notify = on_commit;
	wl_signal_add(&ws->commit_signal, &s->commit);
}

void
motion_surface_removed(struct layout_surface *s)
{
	motion_surface_left(s);
	wl_list_remove(&s->commit.link);
}

void
motion_refresh_all(void)
{
	struct layout_surface *s;

	wl_list_for_each(s, &layout_surfaces, link)
		refresh_surface(s, now_of(s->placed));
}

/* The engine's clock */

static void
wake(struct output_layer *ol)
{
	if (!wl_list_empty(&ol->anim.link))
		return;
	wl_list_insert(&ol->output->animation_list, &ol->anim.link);
	weston_output_schedule_repaint(ol->output);
}

void
motion_wake_all(void)
{
	struct output_layer *ol;

	wl_list_for_each(ol, &output_layers, link)
		wake(ol);
}

static struct wl_event_source *hide_source;

/* A fade out ends in a hide, which needs a commit. A commit from inside
 * weston's repaint would rebuild ivi-layout's layers while the repaint
 * runs, so the hide waits for the event loop's next idle point, which
 * comes before the next repaint. The view's alpha goes back to one
 * first, because ivi-layout reuses the view when the surface is shown
 * again. */
static void
finish_hides(void *data)
{
	struct layout_surface *s;
	struct weston_view *view;
	bool any = false;

	(void)data;
	hide_source = NULL;
	wl_list_for_each(s, &layout_surfaces, link) {
		if (!s->hiding || s->fade.running)
			continue;
		view = top_view_of(s);
		detach(s);
		if (view)
			weston_view_set_alpha(view, 1.0);
		s->hiding = false;
		ivi->surface_set_visibility(s->ivisurf, false);
		any = true;
	}
	if (any)
		layout_commit();
}

static void
on_frame(struct weston_animation *anim, struct weston_output *output,
	 const struct timespec *time)
{
	struct output_layer *ol = wl_container_of(anim, ol, anim);
	struct layout_surface *s;
	bool busy = false, hide_due = false;

	arrangement_frame_begin(ol, time);
	wl_list_for_each(s, &ol->stack, stack_link) {
		motion_tick(&s->fade, time);
		motion_tick(&s->move, time);
		refresh_surface(s, time);
	}

	wl_list_for_each(s, &ol->stack, stack_link) {
		if (motion_done(&s->fade, time)) {
			s->fade.running = false;
			hide_due |= s->hiding;
		}
		if (motion_done(&s->move, time))
			s->move.running = false;
		busy |= s->fade.running || s->move.running;
	}
	arrangement_frame_done(ol, time);
	busy |= arrangement_output_busy(ol);

	if (hide_due && !hide_source)
		hide_source = wl_event_loop_add_idle(
			wl_display_get_event_loop(compositor->wl_display),
			finish_hides, NULL);

	if (busy) {
		weston_output_schedule_repaint(output);
	} else {
		wl_list_remove(&anim->link);
		wl_list_init(&anim->link);
	}
}

/* Commits and the operator's transitions */

static bool
shown_on(struct layout_surface *s, struct output_layer *ol)
{
	return s->placed == ol && ivi->get_properties_of_surface(s->ivisurf)->visibility;
}

/* A place states the transition that its commit runs. A fade shows a
 * surface that was hidden or leaving, and does nothing to a surface
 * that shows already, as ivi-layout's own fade did. A move glides from
 * where the region shows now, on the same output, and a surface that
 * was not showing there has nothing to glide from, so it appears in
 * its region at once. */
void
motion_place(struct layout_surface *s, struct output_layer *ol,
	     const char *word, uint32_t ms)
{
	s->pending_ms = ms;
	if (strcmp(word, "fade") == 0)
		s->pending = !shown_on(s, ol) || s->hiding ? PENDING_FADE_IN : PENDING_CUT;
	else if (strcmp(word, "move") == 0 && shown_on(s, ol) &&
		 committed_region(s).w > 0) {
		s->pending = PENDING_MOVE;
		s->pending_from = region_of(s, now_of(ol));
	} else
		s->pending = PENDING_CUT;
}

/* Returns whether the hide takes the surface off at the commit. A fade
 * keeps the surface visible to ivi-layout until the fade ends, and the
 * client keeps drawing through it. */
bool
motion_hide(struct layout_surface *s, const char *word, uint32_t ms)
{
	s->pending_ms = ms;
	if (strcmp(word, "fade") == 0 && s->placed &&
	    shown_on(s, s->placed)) {
		s->pending = PENDING_FADE_OUT;
		return false;
	}
	s->pending = PENDING_CUT;
	return true;
}

/* Starts the motion a place or a hide asked for, from where the surface
 * shows at the commit. */
static void
arm(struct layout_surface *s)
{
	const struct timespec *now = now_of(s->placed);
	double alpha = alpha_of(s, now);

	switch (s->pending) {
	case PENDING_NONE:
		return;
	case PENDING_CUT:
		s->fade.running = false;
		s->move.running = false;
		s->hiding = false;
		break;
	case PENDING_FADE_IN:
		s->alpha_from = s->fade.running || s->hiding ? alpha : 0.0;
		s->alpha_to = 1.0;
		s->hiding = false;
		motion_begin(&s->fade, s->pending_ms);
		break;
	case PENDING_FADE_OUT:
		s->alpha_from = alpha;
		s->alpha_to = 0.0;
		s->hiding = true;
		motion_begin(&s->fade, s->pending_ms);
		break;
	case PENDING_MOVE:
		s->move_from = s->pending_from;
		motion_begin(&s->move, s->pending_ms);
		break;
	}
	s->pending = PENDING_NONE;
}

/* Every commit in the module goes through here. ivi-layout sets each
 * mapped view's alpha from its opacity on a commit, so the engine sets
 * the alpha and the transform of every surface again before weston
 * repaints. */
void
layout_commit(void)
{
	struct layout_surface *s;

	ivi->commit_changes();
	wl_list_for_each(s, &layout_surfaces, link)
		arm(s);
	motion_refresh_all();
	motion_wake_all();
	arrangement_layout_changed();
}

/* Outputs and surfaces that leave */

void
motion_output_added(struct output_layer *ol)
{
	ol->anim.frame = on_frame;
	wl_list_init(&ol->anim.link);
}

/* weston does not drain an output's animation list when the output
 * goes, so the engine leaves it here, before the list is freed. */
void
motion_output_dropped(struct output_layer *ol)
{
	struct layout_surface *s;

	wl_list_remove(&ol->anim.link);
	wl_list_init(&ol->anim.link);
	wl_list_for_each(s, &ol->stack, stack_link)
		motion_surface_left(s);
	arrangement_output_dropped(ol);
}

/* A surface that leaves its output, or leaves the compositor, stops
 * moving and drops its node from the view it leaves behind. */
void
motion_surface_left(struct layout_surface *s)
{
	detach(s);
	drop_child_nodes(s);
	s->fade.running = false;
	s->move.running = false;
	s->hiding = false;
	s->pending = PENDING_NONE;
}
