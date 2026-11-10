# Browser motion that is not decided

When a press opens a page in the media browser, the page's backdrop
appears in one frame over the wall that opened it. Every other motion
that a press makes in the browser is built, but this fade has no length
and no design. The same is true of a few motions that
[library-operator plan 23](../completed/23-motion.md) put aside, of the
frame budget that every motion runs inside, and of the shared durations
that `brand/motion.md` must state for every surface.

## What is built

Plan 23 built the focus outline's glide and breath, the eased scroll of
every row and wall, and the fades of the lights, the loading curtain,
and the volume row. Each motion is a pure function of the clock: it
takes its share of its length from `look::share` and eases it with
`look::eased`, a smoothstep (`media-browser/src/look.rs:367` and
`:373`). The lengths are constants in `look.rs`, such as `GLIDE` (110
ms, `look.rs:248`), `SCROLL`, `DEPARTURE`, and the breath's constants.
`next_frame` (`browser.rs:1206`) asks for frames only while a motion
runs.

## The page backdrop's fade

Plan 23's design asks for it: a page's backdrop fades in over the wall
that opened it. The rules that it follows are the rules of every other
motion in the browser:

- The fade is a pure function of the clock: a start, a length, and an
  easing. No state passes from one frame to the next. The screen
  requests a frame while it runs, and requests none after it ends.
- It never waits for a decode. When the art is not ready, the fade runs
  over the placeholder, and the art appears when its decode completes.
- Its length is short, and it is a constant in `look.rs`, because the
  motion must not fall behind a remote button that a person presses and
  holds.

No length is chosen for the fade.

## The motions put aside

- The film steps back when it is paused, in the same arrangement as the
  up-next step-back that
  [media-operator plan 44](../../../media-operator/plans/completed/44-the-display-arranges-its-claim.md)
  built.
- A poster that opens into the film at the start of a play. It is
  parked because the image pull and the film's start take a variable
  time, so the motion could end on a wait.

## The frame budget

Whether the machine's compositor and GPU keep a steady frame rate during
a wall scroll while decodes run, and what the frame budget is.
[Root plan 85](../../../plans/completed/85-motion.md) expected its
measurements to give the browser this budget. No plan records a
measurement of it.

## The shade's fade

Whether the shade, the overlay that covers a sleeping screen, also fades
in on sleep and out on wake. `media-operator` decides that.

## The shared durations in `brand/motion.md`

Step 6 of root plan 85 asks `brand/motion.md` to state the durations,
the curves, and the rules that every surface follows, next to the rules
for the mark. `brand/motion.md` states the rules of the mark and of the
focus outline. It does not state the durations and curves that every
surface shares. The browser's own lengths are only in `look.rs`.

## Status

Each item needs a design decision. The page backdrop's fade needs a
length and a curve. The motions put aside need a decision on whether
they are wanted. The frame budget needs a measurement on a small
machine. The durations in `brand/motion.md` need a decision on which of
the browser's lengths every surface shares.
