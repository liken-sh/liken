# Weston 16

Plan 30. Proposed and built on 2026-10-24. Steps 2 and 3 of
[How it is proved](#how-it-is-proved) have not run.

display-operator runs the compositor from the weston image, and that
image takes Weston from the Debian snapshot that every base installs
from. [Root plan 82](../../../plans/completed/82-debian-sid.md) moves the snapshot
from trixie to sid, so the compositor moves from Weston 14.0.2 to
Weston 16.0.0. This plan records why the move fixes a fault in the
picture, and what changes in this operator and in the weston image.

## The fault

With subtitles on, the picture shifts color each time a subtitle line
appears, and shifts back when the line leaves. On 2026-10-24 a Player
on a home cluster played a 1080p film with subtitles, with the
`drm-backend` log scope on for 75 seconds:

- With no subtitle, the `P010` video went to a primary plane, and the
  display hardware converted YUV to RGB.
- mpv draws subtitles into an SHM buffer, and an SHM buffer cannot go
  on a plane. While a subtitle was up, Weston logged `not assigning
  view to plane (no buffer available)` for it, then `occluded by
  renderer views` for the video under it, so the GL renderer drew
  both.
- The log switched between `Using plane-only state composition` and
  `Using mixed state composition` in step with the subtitle lines.

Weston 14 wrote no `COLOR_ENCODING` or `COLOR_RANGE` in any atomic
commit, so the plane converted with its default. i915 creates each
universal plane with the default BT.709 at limited range
(`drivers/gpu/drm/i915/display/skl_universal_plane.c` on Linux
master). Weston 14's GL shader hard-codes BT.601. So the frames with a
subtitle are the ones with the wrong colors. The shift started when
media-operator moved mpv to `--vo=dmabuf-wayland`, because before
that mpv converted every frame itself.

Weston 15 added `wp_color_representation_v1`. In 16.0.0 the GL
renderer advertises it (`gl-renderer.c` sets `WESTON_CAP_COLOR_REP`),
and both paths take the coefficients from the surface, with BT.709 at
limited range as the default. The DRM backend chooses the plane's
`COLOR_ENCODING` and `COLOR_RANGE` for each YUV framebuffer
(`backend-drm/state-propose.c`) and writes them in the commit
(`backend-drm/kms.c`).

## What changes

The controller module, `weston/layout/liken-layout.c`:

- `ivi-layout-export.h` comes from the 16.0.0 tag. Weston 16 inserts
  `add_listener_desktop_surface_ping_timeout` in the middle of
  `struct ivi_layout_interface`, so a module built with the 14.0
  header calls the wrong function for every member after it.
- The module calls `screen_ready` for each output after it adds the
  output's background. Weston 16 repaints an output only after
  `weston_output_set_ready` marks it ready, and in ivi-shell only the
  controller marks it. Without the call, every output stays dark.
- `weston_curtain_params` takes a `char *label` in place of
  `get_label`, and Weston frees the label with the curtain. The module
  passes a copy on the heap.

The weston image: `weston-closure.sh` names `libweston-16`, and the
module set is counted again from `dpkg -L weston`. The layout smoke
check builds on the sid snapshot against `libweston-16-dev`. Weston
16's `weston-screenshooter` writes a file even when the compositor
refuses the capture, so the smoke client checks the refusal from the
log and not from a missing file.

This operator: the flags, the `weston.ini` keys, and the `drm-backend`
log scope it writes are all in 16.0.0. The capture client binds
version 1 of `weston-output-capture`, which 16.0.0 serves at version
2, so it keeps working. Weston 16 still destroys the compositor before
the display, so the card gate holds. The comments that name 14.0.2
change to name 16.0.0. Weston 16's flight recorder no longer
subscribes `drm-backend`, so the scope costs nothing until
`WESTON_LOG_SCOPES` names it.

`media-operator/local/weston-nested` copies the wayland backend from
`libweston-16`.

## How it is proved

1. The weston image builds, and `weston/smoke/weston.sh` and the
   layout smoke pass.
2. On the `liken-1` testbed, display-operator's dev build starts the
   compositor, every output lights, a Player's idle screen draws, and
   `wayland-info` lists `wp_color_representation_manager_v1`.
3. On a Player of the house cluster, with `WESTON_LOG_SCOPES` set to
   `drm-backend`, a plane-only commit sets `COLOR_ENCODING`, and a film
   with subtitles shows no change in color when a subtitle line
   appears and leaves.
