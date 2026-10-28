# 85, Motion on the screen

Proposed on 2026-10-27, and built through step 3 on 2026-10-27.
Touches `weston`, `display-operator`, `media-operator`,
`library-operator`, and `brand`.

Steps 1 and 2 are built by
[display-operator plan 31](../../display-operator/plans/completed/31-an-arranger-inside-each-region.md),
and the step-back of step 3 by
[media-operator plan 44](../../media-operator/plans/completed/44-the-display-arranges-its-claim.md).
The rest continues in the component plans.
[library-operator plan 84](../../library-operator/plans/84-a-larger-up-next-card.md)
holds the card's timing and content from step 3.
[library-operator plan 23](../../library-operator/plans/23-motion.md)
holds step 4, and with it the part of step 6 that is not written:
`brand/motion.md` states the rules of the mark and the focus outline,
and not yet the durations and curves that every surface shares. Step
5 waits for a workload that needs a moving `Layout` region, and the
open problem
[a layout region does not move](../../display-operator/plans/open-problems/a-layout-region-does-not-move.md)
holds it.

## The problem

Almost nothing on a `liken` screen moves. The media browser changes
pages in one frame, the display over a film only fades, and the
compositor's own transitions step at 30 frames a second with a sine
ease, whatever the panel's refresh rate is
(`ivi-layout-transition.c:163` in weston 16.0.0). A person reads an
instant change as a glitch and a smooth one as the system answering
them.

The up-next card shows the cost. The design for
[library-operator plan 84](../../library-operator/plans/84-a-larger-up-next-card.md)
has the film step back into a frame at the credits while the next work
takes the right half of the screen. No component can do that smoothly
today. When mpv moves its own picture, the move lands only on the
film's frames, so a 24 fps film moves in 24 uneven steps a second. Each
step also rebuilds a full-screen buffer in mpv 0.41, and on `liken-1`
the player container ran out of memory after about 165 steps.
[library-operator plan 23](../../library-operator/plans/23-motion.md),
motion in the browser, waits on the same missing piece: nobody has
measured whether a machine keeps its frame rate while something moves.

## The design

1. **The compositor runs motion at the panel's refresh.** The weston
   module that `display-operator` ships, `liken-layout.c`, gets one
   animation engine. It advances on each repaint of the output, so every
   step lands on a vsync, and it eases with the smoothstep that the
   browser already uses. The module's existing fades and glides between
   `Layout` regions move onto the engine, with no change to the
   `Layout` API.
2. **Each part of the screen has one owner.** The `Layout` that a
   `Display` names owns the screen: `display-operator` gives each claim
   a region, and no workload can move a region. Inside a region, the
   claim's surfaces belong to one arranger, the first client of the
   claim to ask for it. `display-operator` shows each claim's arranger
   and its current arrangement in the `Display`'s status, and posts an
   `Event` when a claim gains or loses its arranger.
3. **An arranger arranges its claim's surfaces.** The module offers a
   Wayland protocol, `liken_arrangement_v1`, on each claim's socket. An
   arrangement names the claim's surfaces by their xdg `app_id` and
   states the rectangle, opacity, stacking, and delay of each. The
   compositor tweens every surface from where it shows to the new
   arrangement on the engine, and a new arrangement starts from wherever
   the surfaces show. A video player can so be built from several
   surfaces of several programs, and each workload on a screen arranges
   only its own region. Video scales and never resizes, so its client
   never reallocates. A panel of text resizes when its tween lands, so
   it redraws sharp. The request reaches the compositor on the client's
   own Wayland connection, with no trip through the API server, so a
   press moves the surfaces in the next frame. When the arranger goes
   away, every surface tweens back to filling the region.
4. **The up-next card is the first arrangement.** `media-operator`'s
   display arranges its own claim: at the credits of a film or an
   episode, the film steps back into a frame and the next work takes
   the right half of the screen. `library-operator` decides when the
   credits are confirmed and what the card says. Plan 84 holds that
   design.
5. **The browser's motion follows the same rules.** Plan 23's focus
   slide, wall scroll, and page fades run inside the browser's own
   surface, so they do not need the compositor's engine. They share its
   rules and its durations and curves, and the measurements of this plan
   give plan 23 its frame budget.
6. **One vocabulary of motion.** `brand/motion.md` states the
   durations, the curves, and the rules that every surface follows, next
   to the rules for the mark that it states now.

The rules that every step follows:

- A motion is a pure function of the clock: a start, a length, and a
  curve. Nothing that moves keeps state from one frame to the next.
- A surface draws frames only while something on it moves.
- Motion translates, fades, and scales whole surfaces. It never scales
  text or art inside a client, because the client rasterizes each at its
  exact size.
- A press gets a visible answer on the next frame.

## The order of the work

Each step is a component plan that cites this one.

1. The engine in `liken-layout.c`, with the existing transitions moved
   onto it (`display-operator`).
2. The arrangement protocol, the arranger in the `Display`'s status,
   and weston built from the upstream release (`display-operator`).
3. The display as its claim's arranger and the step-back at the
   credits (`media-operator`), then the card's timing and content
   (`library-operator` plan 84).
4. The browser's motion (`library-operator` plan 23).
5. Motion of a `Layout` region for any workload, such as a picture in
   picture or a camera in a corner, on the same engine. This step waits
   for a workload that needs it.

## Choices

- **The compositor moves the film, not mpv.** mpv's `video-zoom` moves
  the picture only when mpv commits a video frame (`vo.c:926-933` in mpv
  0.41.0). Its `display-resample` sync commits on every vsync, but it
  changes the timing and the audio of every film to serve one animation.
- **A protocol, not a `Layout` field.** A `Layout` change goes through
  the API server and `display-operator`'s pass, a few hundred
  milliseconds each way, which is too slow for Back. A `Layout` also
  belongs to the person who writes it, and a step-back region in every
  `Layout` would make each one carry a media feature.
- **Whole arrangements, not commands for one surface.** Each request
  states every surface of the claim, so several surfaces move as one,
  and an interrupted tween needs no bookkeeping in the client.
- **One arranger for each claim.** A claim whose surfaces anyone could
  move would have no owner, and a client that arranged once at start
  would silently undo another's arrangement.
- **Surfaces are named by `app_id`.** Every client already sets one,
  and a program that does not know the protocol, such as mpv, can be
  told which to set. A tag that each client set through the protocol
  would leave such a program out.
- **The display stays a separate client.** Embedding the player in the
  display would put the picture and the controls in one crash domain,
  and libmpv's render API offers no dmabuf path for the dmabuf hand-off
  that the player depends on.
- **The ivi-layout transitions move onto the engine** instead of
  staying beside it. Two animation paths in one module would step at two
  rates and leave the old 30 fps steps in every existing fade.

## Evidence

On `liken-1` on 2026-10-27, mpv's `video-zoom` was stepped 55 times in
900 ms over its IPC socket during a 2.39:1 24 fps HEVC film decoded
through VA-API on a 1080p60 panel. mpv dropped and delayed no frames,
and its CPU went from 12.5 percent to about 45 percent of a core during
each glide. A person watching saw jerks along the way. The player
container was killed for memory against its 1 GiB limit after about 165
steps, in both runs. Upstream mpv changed `vo_dmabuf_wayland` on
2026-10-26 to rebuild its OSD buffer only when its size changes
(`b3c1e1c99`), which is not in a release.

A prototype of the module scaled mpv's film on `liken-1`'s `stick-1`,
an Apollo Lake machine with a 1080p60 panel, on 2026-10-27. The first
version scaled mpv's whole window, including its solid black backdrop.
weston then had no solid view to lower to the CRTC background and wrote
`BACKGROUND_COLOR` 0, which i915 refused in every atomic commit, so the
picture froze while the sound played on. The module now leaves a solid
backdrop at full size and scales the subsurfaces over it. The same
refusal froze the released compositor for 12 to 25 commits at the start
of every film: weston 16.0.0 writes 0 whenever no solid view covers the
output, and weston 16.0.1 changed the default to opaque black
(`ed403635`). With weston 16.0.2 built from source, a film started with
no refused commit. Over six step-backs and restores of 900 ms each,
each landed one frame after its length, mpv used 11 to 18 percent of a
core as it does with nothing moving, weston used 4 to 10 percent, the
GPU stayed between 100 and 450 MHz, and the package stayed between 69
and 76 degrees.

A spike on a GNOME desktop bound a global on the Wayland connection of
an iced 0.14 window through `wayland-backend`'s foreign display, while
iced kept drawing. A second connection would not serve, because the
module tells clients apart by connection.

## What is not decided

- Whether the film changes between a scanout plane and the GPU
  composite during a step-back, and whether that shows.
- The memory bandwidth and the image quality when a 4K film is scaled
  down on a small machine.
