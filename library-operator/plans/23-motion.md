# Animation in the media browser

Plan 23. This is a stub for a later agent to design. It adds animation to
the media browser: the focus outline slides, pages open, and walls
scroll smoothly. It draws no frame that the machine does not need.

## The problem

Every change on the screen is instant today. Focus jumps from one slot to
the next, a page appears in one frame, and a wall scrolls by a whole
row. Plan 22 chose an outline for focus over an enlarged slot for two
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
