# Motion

Plan 23. A stub for a later agent to shape. It gives the media browser
movement: focus that slides, pages that open, and walls that glide,
without a frame the box does not need.

## The problem

Every change on the screen is a cut today. Focus jumps from one slot to
the next, a page appears in one frame, and a wall scrolls by a whole
row. Plan 22 chose an outline for focus over a grown slot for two
reasons: a grown slot needs a second decode and flashes while it lands,
and a jump between two sizes reads as a glitch where a slide would read
as motion. The outline leaves the door open. A slide, a fade, or a
scale can draw over the one decoded image the slot already holds.

The loop is the constraint. The browser draws only on events, and
`next_frame` answers nothing while the screen is still, which is what
keeps a one-gigabyte box cool and the remote instant. Motion has to
schedule frames only while something moves, and end.

## The shape

- Every animation is a pure function of the clock: a start, a length,
  and an easing, with no state a frame has to carry to the next. The
  screen asks for a frame while any animation runs and for none when the
  last one ends.
- The first motions are the ones a person feels on every press: the
  focus outline slides from the old slot to the new one, a wall's scroll
  eases over a few frames instead of jumping a row, and a page's
  backdrop fades in over the wall it opened from.
- A motion never waits on a decode. Where the art is not there yet, the
  motion runs over the placeholder and the art lands when it lands.
- Lengths are short and one set of constants in `look.rs`, because a
  remote pressed and held has to stay ahead of the animation.

## What is not decided

Whether the box's compositor and GPU hold a steady frame rate during a
wall scroll with decodes in flight, and what the frame budget is. How a
held key interacts with an animation that has not ended. Whether the
shade's sleep and wake take a fade too, which is `media-operator`'s call.
