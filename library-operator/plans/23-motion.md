# Animation in the media browser

Plan 23. This is a stub for a later agent to design. It adds animation to
the media browser: the focus outline slides, pages open, and walls
scroll smoothly. It draws no frame that the machine does not need.

Status on 2026-10-04: the clock that the design below asks for is
built, and the first fades run on it. The harness moves each screen's
clock with `Screen::tick` (`media-browser/src/harness.rs`). Every
motion reads a share of its own length from `look::share` and eases it
with `look::eased`, a smoothstep (`media-browser/src/look.rs`). The
lights dim and lift as a film comes and goes (`screens/lights.rs`).
The loading state's curtain and mark fade in and out over the page
(`screens/loading.rs`, `views/curtain.rs`, cc7595a6). The volume row
fades in and out (`views/volume.rs`). `next_frame` asks for
frames only while one of these runs (`browser.rs`). The animations of a
button press are not built: the focus outline's slide, the wall's
eased scroll, and the page backdrop's fade. The rules for a held key
are not decided.

## The problem

Every change that a button press makes in the browser is instant. Focus
jumps from one slot to the next, a page appears in one frame, and a
wall scrolls by a whole row. Plan 22 chose an outline for focus over an enlarged slot for two
reasons. An enlarged slot needs a second decode and flashes while that
decode completes. Also, a jump between two sizes looks like a glitch,
where a slide looks like motion. The outline permits animation later: a
slide, a fade, or a scale can draw over the one decoded image that the
slot already has.

The render loop limits the design. The browser draws only on events,
and `next_frame` requests no frame while the screen is still. This keeps
a one-gigabyte machine cool and the remote response instant. Animation
must request frames only while something moves, and must stop
requesting them when the motion ends.

## Design

- Every animation is a pure function of the clock: a start, a length,
  and an easing. No state passes from one frame to the next. The screen
  requests a frame while any animation runs, and requests none after the
  last one ends.
- The first animations are the ones a person sees on every button press.
  The focus outline slides from the old slot to the new one. A wall's
  scroll eases over a few frames instead of jumping a row. A page's
  backdrop fades in over the wall that opened it.
- An animation never waits for a decode. When the art is not ready, the
  animation runs over the placeholder, and the art appears when its
  decode completes.
- Lengths are short, and they are one set of constants in `look.rs`,
  because the animation must not fall behind a remote button that a
  person presses and holds.

## What is not decided

Whether the machine's compositor and GPU keep a steady frame rate during
a wall scroll while decodes run, and what the frame budget is. How a
held key interacts with an animation that has not ended. Whether the
shade, the overlay that covers a sleeping screen, also fades in on
sleep and out on wake. `media-operator` decides that.
