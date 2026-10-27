/*
 * liken-arrange-drill drives liken_arrangement_v1 by hand, and it is the
 * client that `layout/smoke.sh arrange` tests the protocol with. It is
 * either an arranger or a stand-in for a film.
 *
 *   liken-arrange-drill [--window <app_id> <aarrggbb>] <socket> <command>...
 *   liken-arrange-drill --film <rrggbb>[,<rrggbb>] [--app-id <app_id>]
 *                       [--pattern] <socket>
 *
 * As an arranger, it connects to one Wayland socket, asks for the
 * claim's arranger, runs its commands in order, prints each event with
 * the time since it started, and exits. The claim's surfaces tween back
 * to the default when it exits, so a drill that wants to watch an
 * arrangement holds it with hold or wait. The commands:
 *
 *   entry APP X Y W H OPACITY FIT DELAY
 *                 add an entry: the rectangle in the region's logical
 *                 pixels, an opacity from 0 to 1, scale or resize, and
 *                 a delay in milliseconds
 *   commit NAME MS CURVE
 *                 commit the entries with the next serial, over MS
 *                 milliseconds on smoothstep, ease_out, ease_in, or
 *                 linear; it does not wait
 *   wait          wait for the landed event of the last commit
 *   hold MS       keep the arranger for MS milliseconds
 *   stdin         read further commands from standard input, one a line
 *
 * --window makes a window of the drill's own, in one colour, with the
 * app_id, which an entry can place like any surface of the claim.
 *
 * As a film, it draws one window with the app_id liken-film, or the
 * one --app-id names, in one colour, redraws at each size the
 * compositor configures, prints each size it draws, and runs until it
 * is killed. With a second colour, the window is built the way mpv's
 * dmabuf-wayland output builds it: the window is a solid backdrop,
 * drawn with wp_single_pixel_buffer_v1 and wp_viewporter when the
 * compositor offers them, and the picture is a subsurface of the
 * second colour over the middle half of the window. --pattern
 * alternates the colour with black in every other pixel.
 *
 * The socket is a name under XDG_RUNTIME_DIR, or an absolute path. The
 * exit status is 0 when every command completed, 2 for a usage error,
 * 3 when the compositor refused the arranger, and 4 when a commit did
 * not land within its duration and delays plus five seconds or the
 * connection failed.
 */
#define _GNU_SOURCE

#include <errno.h>
#include <poll.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

#include <wayland-client.h>

#include "liken-arrangement-v1-client-protocol.h"
#include "single-pixel-buffer-v1-client-protocol.h"
#include "viewporter-client-protocol.h"
#include "xdg-shell-client-protocol.h"

/* A landed event that does not come within the arrangement's length
 * plus this long means the compositor is not repainting. */
#define LANDED_SLACK_MS 5000

static struct wl_display *display;
static struct wl_compositor *wl_compositor_global;
static struct wl_shm *shm;
static struct xdg_wm_base *wm_base;
static struct liken_arranger_manager_v1 *manager;
static struct wl_subcompositor *subcompositor;
static struct wp_viewporter *viewporter;
static struct wp_single_pixel_buffer_manager_v1 *single_pixel;
static struct liken_arranger_v1 *arranger;
static struct timespec started;

static bool granted, refused;
static uint32_t last_landed, last_serial;
static uint32_t longest_ms;

static double
elapsed_ms(void)
{
	struct timespec now;

	clock_gettime(CLOCK_MONOTONIC, &now);
	return (double)(now.tv_sec - started.tv_sec) * 1000.0 +
	       (double)(now.tv_nsec - started.tv_nsec) / 1e6;
}

static void
say(const char *format, ...)
	__attribute__((format(printf, 1, 2)));

static void
say(const char *format, ...)
{
	va_list ap;

	printf("t=+%.1fms ", elapsed_ms());
	va_start(ap, format);
	vprintf(format, ap);
	va_end(ap);
	putchar('\n');
	fflush(stdout);
}

/* Waiting */

/* When set, a wait also ends when standard input has bytes to read. */
static bool watch_stdin;
static bool stdin_ready;

/* Reads and dispatches events until done() holds or the deadline
 * passes. A deadline below zero waits forever. */
static bool
wait_until(bool (*done)(void), double deadline_ms)
{
	struct pollfd fds[2] = {
		{ .fd = wl_display_get_fd(display), .events = POLLIN },
		{ .fd = STDIN_FILENO, .events = POLLIN },
	};
	double left;
	int ready;

	for (;;) {
		if (done && done())
			return true;
		while (wl_display_prepare_read(display) != 0)
			wl_display_dispatch_pending(display);
		if (wl_display_flush(display) < 0 && errno != EAGAIN) {
			wl_display_cancel_read(display);
			return false;
		}
		left = deadline_ms < 0 ? -1 : deadline_ms - elapsed_ms();
		if (deadline_ms >= 0 && left <= 0) {
			wl_display_cancel_read(display);
			return !done;
		}
		ready = poll(fds, watch_stdin ? 2 : 1, left < 0 ? -1 : (int)left + 1);
		if (ready < 0 && errno != EINTR) {
			wl_display_cancel_read(display);
			return false;
		}
		if (watch_stdin && fds[1].revents) {
			stdin_ready = true;
			wl_display_cancel_read(display);
			return true;
		}
		if (ready > 0 && fds[0].revents) {
			if (wl_display_read_events(display) < 0)
				return false;
		} else {
			wl_display_cancel_read(display);
		}
		if (wl_display_dispatch_pending(display) < 0)
			return false;
	}
}

/* Buffers */

struct buffer {
	struct wl_buffer *wl;
	void *data;
	size_t size;
};

static void
buffer_release(void *data, struct wl_buffer *wl)
{
	struct buffer *b = data;

	wl_buffer_destroy(wl);
	munmap(b->data, b->size);
	free(b);
}

static const struct wl_buffer_listener buffer_listener = {
	.release = buffer_release,
};

/* paint() gets each pixel's position and returns its ARGB8888 value. */
static struct wl_buffer *
make_buffer(int32_t width, int32_t height,
	    uint32_t (*paint)(int32_t x, int32_t y, int32_t w, int32_t h))
{
	struct buffer *b = calloc(1, sizeof *b);
	struct wl_shm_pool *pool;
	uint32_t *pixels;
	int32_t stride = width * 4, x, y;
	int fd;

	if (!b)
		return NULL;
	b->size = (size_t)stride * height;
	fd = memfd_create("liken-arrange-drill", MFD_CLOEXEC);
	if (fd < 0 || ftruncate(fd, b->size) < 0) {
		free(b);
		return NULL;
	}
	b->data = mmap(NULL, b->size, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
	if (b->data == MAP_FAILED) {
		close(fd);
		free(b);
		return NULL;
	}
	pixels = b->data;
	for (y = 0; y < height; y++)
		for (x = 0; x < width; x++)
			pixels[y * width + x] = paint(x, y, width, height);

	pool = wl_shm_create_pool(shm, fd, b->size);
	b->wl = wl_shm_pool_create_buffer(pool, 0, width, height, stride,
					  WL_SHM_FORMAT_ARGB8888);
	wl_shm_pool_destroy(pool);
	close(fd);
	wl_buffer_add_listener(b->wl, &buffer_listener, b);
	return b->wl;
}

/* Windows */

struct window {
	struct wl_surface *surface;
	struct xdg_surface *xdg_surface;
	struct xdg_toplevel *toplevel;
	int32_t width, height;
	int32_t next_width, next_height;
	bool drawn;
	uint32_t (*paint)(int32_t x, int32_t y, int32_t w, int32_t h);
	/* A film with a picture: the backdrop's viewport, when the
	 * backdrop is a single-pixel buffer, and the picture's
	 * subsurface. */
	struct wp_viewport *viewport;
	struct wl_surface *picture;
	struct wl_subsurface *picture_subsurface;
};

static uint32_t film_colour;
static uint32_t picture_colour;

static bool film_pattern;

/* A film with --pattern alternates its colour with black in each
 * pixel, so a screenshot tells a buffer drawn at its size on screen,
 * whose pixels are all one or the other, from a scaled one, whose
 * filter blends them. */
static uint32_t
paint_film(int32_t x, int32_t y, int32_t w, int32_t h)
{
	(void)w, (void)h;
	if (film_pattern && (x + y) % 2)
		return 0xff000000u;
	return 0xff000000u | film_colour;
}

static uint32_t
paint_picture(int32_t x, int32_t y, int32_t w, int32_t h)
{
	(void)x, (void)y, (void)w, (void)h;
	return 0xff000000u | picture_colour;
}

static uint32_t
channel(uint32_t colour, int shift)
{
	return (uint32_t)(((uint64_t)((colour >> shift) & 0xff) * UINT32_MAX) / 0xff);
}

/* The backdrop and the picture of a film with a picture. The picture
 * covers the middle half of the window and commits in desynchronized
 * mode, as a video subsurface does. */
static struct wl_buffer *
draw_film_with_picture(struct window *win)
{
	struct wl_buffer *backdrop, *picture;

	picture = make_buffer(win->width / 2, win->height / 2, paint_picture);
	if (!picture)
		return NULL;
	wl_subsurface_set_position(win->picture_subsurface, win->width / 4, win->height / 4);
	wl_surface_attach(win->picture, picture, 0, 0);
	wl_surface_damage_buffer(win->picture, 0, 0, INT32_MAX, INT32_MAX);
	wl_surface_commit(win->picture);

	if (win->viewport) {
		backdrop = wp_single_pixel_buffer_manager_v1_create_u32_rgba_buffer(
			single_pixel, channel(film_colour, 16), channel(film_colour, 8),
			channel(film_colour, 0), UINT32_MAX);
		wp_viewport_set_destination(win->viewport, win->width, win->height);
		return backdrop;
	}
	return make_buffer(win->width, win->height, paint_film);
}

static uint32_t window_colour;

/* The arranger's own window, in one ARGB colour. */
static uint32_t
paint_window(int32_t x, int32_t y, int32_t w, int32_t h)
{
	(void)x, (void)y, (void)w, (void)h;
	return window_colour;
}

static void
xdg_surface_configure(void *data, struct xdg_surface *xdg_surface, uint32_t serial)
{
	struct window *win = data;
	struct wl_buffer *buffer;

	xdg_surface_ack_configure(xdg_surface, serial);
	if (win->next_width > 0 && win->next_height > 0) {
		win->width = win->next_width;
		win->height = win->next_height;
	}
	buffer = win->picture ? draw_film_with_picture(win)
			      : make_buffer(win->width, win->height, win->paint);
	if (!buffer) {
		fprintf(stderr, "liken-arrange-drill: no buffer at %dx%d\n",
			win->width, win->height);
		exit(4);
	}
	wl_surface_attach(win->surface, buffer, 0, 0);
	wl_surface_damage_buffer(win->surface, 0, 0, INT32_MAX, INT32_MAX);
	wl_surface_commit(win->surface);
	win->drawn = true;
	say("drawn %dx%d", win->width, win->height);
}

static const struct xdg_surface_listener xdg_surface_listener = {
	.configure = xdg_surface_configure,
};

static void
toplevel_configure(void *data, struct xdg_toplevel *toplevel,
		   int32_t width, int32_t height, struct wl_array *states)
{
	struct window *win = data;

	(void)toplevel, (void)states;
	win->next_width = width;
	win->next_height = height;
}

static void
toplevel_close(void *data, struct xdg_toplevel *toplevel)
{
	(void)data, (void)toplevel;
	exit(0);
}

static void
toplevel_configure_bounds(void *data, struct xdg_toplevel *toplevel,
			  int32_t width, int32_t height)
{
	(void)data, (void)toplevel, (void)width, (void)height;
}

static void
toplevel_wm_capabilities(void *data, struct xdg_toplevel *toplevel,
			 struct wl_array *capabilities)
{
	(void)data, (void)toplevel, (void)capabilities;
}

static const struct xdg_toplevel_listener toplevel_listener = {
	.configure = toplevel_configure,
	.close = toplevel_close,
	.configure_bounds = toplevel_configure_bounds,
	.wm_capabilities = toplevel_wm_capabilities,
};

static void
wm_base_ping(void *data, struct xdg_wm_base *base, uint32_t serial)
{
	(void)data;
	xdg_wm_base_pong(base, serial);
}

static const struct xdg_wm_base_listener wm_base_listener = {
	.ping = wm_base_ping,
};

static struct window *drawing;

static bool
window_drawn(void)
{
	return drawing->drawn;
}

static struct window *
make_window(const char *app_id,
	    uint32_t (*paint)(int32_t x, int32_t y, int32_t w, int32_t h),
	    bool with_picture)
{
	struct window *win = calloc(1, sizeof *win);

	if (!win || !wl_compositor_global || !shm || !wm_base) {
		fprintf(stderr, "liken-arrange-drill: the compositor lacks wl_compositor, wl_shm or xdg_wm_base\n");
		exit(4);
	}
	win->width = 640;
	win->height = 360;
	win->paint = paint;
	win->surface = wl_compositor_create_surface(wl_compositor_global);
	if (with_picture) {
		if (!subcompositor) {
			fprintf(stderr, "liken-arrange-drill: the compositor lacks wl_subcompositor\n");
			exit(4);
		}
		win->picture = wl_compositor_create_surface(wl_compositor_global);
		win->picture_subsurface = wl_subcompositor_get_subsurface(
			subcompositor, win->picture, win->surface);
		wl_subsurface_set_desync(win->picture_subsurface);
		if (viewporter && single_pixel)
			win->viewport = wp_viewporter_get_viewport(viewporter, win->surface);
	}
	win->xdg_surface = xdg_wm_base_get_xdg_surface(wm_base, win->surface);
	xdg_surface_add_listener(win->xdg_surface, &xdg_surface_listener, win);
	win->toplevel = xdg_surface_get_toplevel(win->xdg_surface);
	xdg_toplevel_add_listener(win->toplevel, &toplevel_listener, win);
	xdg_toplevel_set_app_id(win->toplevel, app_id);
	wl_surface_commit(win->surface);

	drawing = win;
	if (!wait_until(window_drawn, elapsed_ms() + 10000)) {
		fprintf(stderr, "liken-arrange-drill: the compositor never configured the window\n");
		exit(4);
	}
	return win;
}

/* The arranger */

static void
arranger_refused(void *data, struct liken_arranger_v1 *a, uint32_t reason)
{
	(void)data, (void)a;
	say("refused %s", reason == LIKEN_ARRANGER_V1_REFUSAL_NOT_A_CLAIM ?
	    "not_a_claim" : "busy");
	refused = true;
}

static void
arranger_granted(void *data, struct liken_arranger_v1 *a)
{
	(void)data, (void)a;
	say("granted");
	granted = true;
}

static void
arranger_region(void *data, struct liken_arranger_v1 *a, int32_t w, int32_t h)
{
	(void)data, (void)a;
	say("region %d %d", w, h);
}

static void
arranger_surfaces(void *data, struct liken_arranger_v1 *a, const char *app_id,
		  uint32_t count)
{
	(void)data, (void)a;
	say("surfaces %s %u", app_id, count);
}

static void
arranger_landed(void *data, struct liken_arranger_v1 *a, uint32_t serial)
{
	(void)data, (void)a;
	say("landed %u", serial);
	last_landed = serial;
}

static const struct liken_arranger_v1_listener arranger_listener = {
	.refused = arranger_refused,
	.granted = arranger_granted,
	.region = arranger_region,
	.surfaces = arranger_surfaces,
	.landed = arranger_landed,
};

static void
registry_global(void *data, struct wl_registry *registry, uint32_t name,
		const char *interface, uint32_t version)
{
	(void)data, (void)version;
	if (strcmp(interface, wl_compositor_interface.name) == 0)
		wl_compositor_global = wl_registry_bind(registry, name,
							&wl_compositor_interface, 4);
	else if (strcmp(interface, wl_shm_interface.name) == 0)
		shm = wl_registry_bind(registry, name, &wl_shm_interface, 1);
	else if (strcmp(interface, wl_subcompositor_interface.name) == 0)
		subcompositor = wl_registry_bind(registry, name,
						 &wl_subcompositor_interface, 1);
	else if (strcmp(interface, wp_viewporter_interface.name) == 0)
		viewporter = wl_registry_bind(registry, name, &wp_viewporter_interface, 1);
	else if (strcmp(interface, wp_single_pixel_buffer_manager_v1_interface.name) == 0)
		single_pixel = wl_registry_bind(registry, name,
						&wp_single_pixel_buffer_manager_v1_interface, 1);
	else if (strcmp(interface, xdg_wm_base_interface.name) == 0) {
		wm_base = wl_registry_bind(registry, name, &xdg_wm_base_interface, 1);
		xdg_wm_base_add_listener(wm_base, &wm_base_listener, NULL);
	} else if (strcmp(interface, liken_arranger_manager_v1_interface.name) == 0)
		manager = wl_registry_bind(registry, name,
					   &liken_arranger_manager_v1_interface, 1);
}

static void
registry_global_remove(void *data, struct wl_registry *registry, uint32_t name)
{
	(void)data, (void)registry, (void)name;
}

static const struct wl_registry_listener registry_listener = {
	.global = registry_global,
	.global_remove = registry_global_remove,
};

/* Commands */

static bool
landed_or_refused(void)
{
	return refused || last_landed == last_serial;
}

static bool
parse_long(const char *word, long *out)
{
	char *end;

	if (!word)
		return false;
	errno = 0;
	*out = strtol(word, &end, 10);
	return !errno && *end == '\0' && end != word;
}

static int
named(const char *word, const char *const *names, int count)
{
	int i;

	for (i = 0; word && i < count; i++)
		if (strcmp(word, names[i]) == 0)
			return i;
	return -1;
}

static const char *const fits[] = { "scale", "resize" };
static const char *const curves[] = { "smoothstep", "ease_out", "ease_in", "linear" };

static int run_stdin(void);

/* Runs one command from words and returns how many words it took, or
 * -1 for a command it does not know. */
static int
run_command(char **words, int *status)
{
	long n[6];
	int fit, curve, i;
	double opacity;
	char *end;

	*status = 0;
	if (strcmp(words[0], "entry") == 0) {
		for (i = 0; i < 4; i++)
			if (!parse_long(words[2 + i], &n[i]))
				return -1;
		if (!words[1] || !words[6])
			return -1;
		opacity = strtod(words[6], &end);
		fit = named(words[7], fits, 2);
		if (*end || fit < 0 || !parse_long(words[8], &n[4]))
			return -1;
		say("entry %s %ld %ld %ld %ld %.2f %s %ld", words[1], n[0], n[1],
		    n[2], n[3], opacity, fits[fit], n[4]);
		liken_arranger_v1_entry(arranger, words[1], n[0], n[1], n[2], n[3],
					wl_fixed_from_double(opacity), fit, n[4]);
		if (n[4] > (long)longest_ms)
			longest_ms = n[4];
		return 9;
	}
	if (strcmp(words[0], "commit") == 0) {
		curve = named(words[3], curves, 4);
		if (!words[1] || !parse_long(words[2], &n[0]) || curve < 0)
			return -1;
		last_serial++;
		say("commit %u %s %ld %s", last_serial, words[1], n[0], curves[curve]);
		liken_arranger_v1_commit(arranger, last_serial, words[1], n[0], curve);
		wl_display_flush(display);
		longest_ms += n[0];
		return 4;
	}
	if (strcmp(words[0], "wait") == 0) {
		if (!wait_until(landed_or_refused, elapsed_ms() + longest_ms + LANDED_SLACK_MS)) {
			say("no landed for %u", last_serial);
			*status = 4;
		}
		longest_ms = 0;
		return 1;
	}
	if (strcmp(words[0], "hold") == 0 && parse_long(words[1], &n[0])) {
		say("hold %ld", n[0]);
		if (!wait_until(NULL, elapsed_ms() + n[0]))
			*status = 4;
		return 2;
	}
	if (strcmp(words[0], "stdin") == 0) {
		*status = run_stdin();
		return 1;
	}
	return -1;
}

static int
run_commands(char **words)
{
	int taken, status = 0;

	while (*words && status == 0 && !refused) {
		taken = run_command(words, &status);
		if (taken < 0) {
			fprintf(stderr, "liken-arrange-drill: no command at \"%s\"\n", words[0]);
			return 2;
		}
		words += taken;
	}
	return refused ? 3 : status;
}

/* Reads one line from standard input while it keeps the connection's
 * events flowing, because events arrive while the drill waits for its
 * next command. */
static bool
read_line(char *line, size_t size)
{
	static char buffer[1024];
	static size_t used;
	char *end;
	ssize_t count;

	for (;;) {
		end = memchr(buffer, '\n', used);
		if (end) {
			size_t length = (size_t)(end - buffer);

			if (length >= size)
				length = size - 1;
			memcpy(line, buffer, length);
			line[length] = '\0';
			used -= (size_t)(end + 1 - buffer);
			memmove(buffer, end + 1, used);
			return true;
		}
		if (used == sizeof buffer)
			return false;
		watch_stdin = true;
		stdin_ready = false;
		if (!wait_until(NULL, -1) || !stdin_ready) {
			watch_stdin = false;
			return false;
		}
		watch_stdin = false;
		count = read(STDIN_FILENO, buffer + used, sizeof buffer - used);
		if (count <= 0)
			return false;
		used += (size_t)count;
	}
}

static int
run_stdin(void)
{
	char line[256], *words[16], *save, *word;
	int count, status;

	say("ready");
	while (read_line(line, sizeof line)) {
		count = 0;
		for (word = strtok_r(line, " \t\n", &save); word && count < 15;
		     word = strtok_r(NULL, " \t\n", &save))
			words[count++] = word;
		words[count] = NULL;
		if (count == 0)
			continue;
		status = run_commands(words);
		if (status != 0)
			return status;
		say("ready");
	}
	return 0;
}

static int
usage(void)
{
	fprintf(stderr,
		"usage: liken-arrange-drill [--window <app_id> <aarrggbb>] <socket> <command>...\n"
		"       liken-arrange-drill --film <rrggbb>[,<rrggbb>] [--app-id <app_id>] [--pattern] <socket>\n"
		"commands: entry APP X Y W H OPACITY FIT DELAY | commit NAME MS CURVE | wait | hold MS | stdin\n");
	return 2;
}

static bool
granted_or_refused(void)
{
	return granted || refused;
}

int
main(int argc, char *argv[])
{
	struct wl_registry *registry;
	const char *film_app_id = "liken-film", *window_app_id = NULL;
	bool film = false, with_picture = false;
	char *comma;
	int i = 1, status;

	clock_gettime(CLOCK_MONOTONIC, &started);
	while (i < argc && strncmp(argv[i], "--", 2) == 0) {
		if (strcmp(argv[i], "--film") == 0 && i + 1 < argc) {
			film = true;
			film_colour = strtoul(argv[i + 1], &comma, 16) & 0xffffffu;
			if (*comma == ',') {
				with_picture = true;
				picture_colour = strtoul(comma + 1, NULL, 16) & 0xffffffu;
			}
			i += 2;
		} else if (strcmp(argv[i], "--app-id") == 0 && i + 1 < argc) {
			film_app_id = argv[i + 1];
			i += 2;
		} else if (strcmp(argv[i], "--pattern") == 0) {
			film_pattern = true;
			i++;
		} else if (strcmp(argv[i], "--window") == 0 && i + 2 < argc) {
			window_app_id = argv[i + 1];
			window_colour = strtoul(argv[i + 2], NULL, 16);
			if (strlen(argv[i + 2]) <= 6)
				window_colour |= 0xff000000u;
			i += 3;
		} else {
			return usage();
		}
	}
	if (argc - i < 1 || (!film && argc - i < 2))
		return usage();

	display = wl_display_connect(argv[i]);
	if (!display) {
		fprintf(stderr, "liken-arrange-drill: no compositor at %s: %s\n",
			argv[i], strerror(errno));
		return 4;
	}
	registry = wl_display_get_registry(display);
	wl_registry_add_listener(registry, &registry_listener, NULL);
	wl_display_roundtrip(display);

	if (film) {
		make_window(film_app_id, paint_film, with_picture);
		say("film %s drawn at %dx%d%s%s", film_app_id, drawing->width, drawing->height,
		    with_picture ? " with a picture" : "",
		    drawing->viewport ? " on a single-pixel backdrop" : "");
		wait_until(NULL, -1);
		return 4;
	}

	if (!manager) {
		fprintf(stderr, "liken-arrange-drill: the compositor offers no liken_arranger_manager_v1\n");
		return 4;
	}
	if (window_app_id)
		make_window(window_app_id, paint_window, false);
	arranger = liken_arranger_manager_v1_get_arranger(manager);
	liken_arranger_v1_add_listener(arranger, &arranger_listener, NULL);
	if (!wait_until(granted_or_refused, elapsed_ms() + 10000) || refused)
		return refused ? 3 : 4;

	status = run_commands(argv + i + 1);
	liken_arranger_v1_destroy(arranger);
	liken_arranger_manager_v1_destroy(manager);
	wl_registry_destroy(registry);
	wl_display_roundtrip(display);
	wl_display_disconnect(display);
	return status;
}
