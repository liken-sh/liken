# Animation in the media browser

Plan 23. It is step 4 of
[root plan 85](../../plans/85-motion.md), which sets the rules and the
durations that every surface shares. It adds animation to
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

Status on 2026-10-27: the focus outline travels and breathes, and rows
and walls scroll eased, on the home page, on every wall, on the
browser's strip, and on the person picker. The title pages, the person
page, and the franchise page do not move yet: each strokes its own mark
at rest, as before. The page backdrop's fade is not built. The
decisions and the lengths are in "The focus outline" below.

Status on 2026-10-28: the outline travels and breathes on every screen.
The movie page, the series page, the person page, and the franchise
page answer `Screen::marked` and `Screen::follow` like the home page
and the walls, every scroll on them eases, and no screen strokes a mark
of its own. The page backdrop's fade is not built.

## The focus outline

When a press moves focus, the outline's four edges move from where the
outline is drawn to the mark of the slot focus moved to. The edge on
the side of travel leads and the opposite edge follows: on a right
press the right edge arrives first, and for a moment the outline spans
both slots and then closes behind the press. Each axis is decided
alone, by the direction the outline's center moves along it, and an
axis the outline does not cross changes size on the lead's length. The
edges ease on `look::eased`, the smoothstep, so nothing overshoots and
the outline never draws past the two slots.

The lengths are constants in `look.rs`, beside `DEPARTURE` and the
lights:

| Constant | Length | Reason |
|---|---|---|
| `LEAD` | 70 ms | From a couch the eye follows a press to the next slot at once, and an outline that arrives late reads as a slow remote. |
| `FOLLOW` | 130 ms | Longer than the lead, so the eye reads which way focus went. |
| `SCROLL` | 110 ms | Between the two, so the row lands while the outline closes on the slot. |
| `HELD_FLOOR` | a third | The shortest share of its length a motion runs at while a key is held. |
| `BREATH_DELAY` | 3 s | The rest after the last motion before the first breath. |
| `BREATH` | 3.6 s | One breath, up to the breath's peak and back. |
| `BREATH_LIFT` | half | How far the peak lifts the accent toward the text color. On `liken-1` on 2026-10-27, a breath that stopped at the full accent brightened the stroke on the glass by about 11 percent, which did not show in a recording. |
| `BREATH_EVERY` | 10 s | A breath starts this often while focus rests. |
| `BREATH_RELEASE` | 80 ms | A press returns a breath to the mark's color this fast. |

The four lengths of a press started at 130, 240, 200, and 150 ms. On a
home TV on 2026-10-28 the glide looked right but felt slower than the
browser had felt with no motion, so each of the four is now about 1.8
times as fast. The breath's own lengths did not change, because a
breath is not an answer to a press.

A row's scroll across and a wall's or the home page's scroll down ease
from their drawn value to the new one over `SCROLL`. The scroll rules
themselves did not change: `strip::offset` and `wall::scrolled` still
decide where a row comes to rest, and the easing decides only how the
drawn value gets there. The outline goes to the slot's mark where the
scroll comes to rest, not where the row is drawn this frame. So where a
long wall or a strip centers its focused slot, the slot's place on the
glass does not change, the outline stands still, and the row slides
under it.

A press during a motion starts the next motion from the value drawn at
that second, so nothing snaps. Presses that arrive faster than
`FOLLOW` are a held key, because a remote repeats a held arrow several
times a second. Each length then shrinks to the time between the two
presses, as a share of `FOLLOW`, and never below `HELD_FLOOR`, so the
outline never falls behind the remote and never turns into a jump of a
frame or two.

A press that changes the page, such as opening a title, back, or home
from a page, is not a move across one page, and the outline lands on
the new page's focus at once. A move between the strip and the page
under it is a move on one page, so it travels. The person picker is a
question over the page, so the outline lands on it, and back off it, at
once. Anything that moves focus without a press, such as a re-read, the
picker the ask raises, or a resize, also lands at once.

While focus rests, the outline breathes: `BREATH_DELAY` after the last
motion ends, its stroke brightens from `look::mark()`, the accent at
0.75 opacity, to `look::breath_peak`, the opaque accent lifted halfway
toward the text color, and back over `BREATH`, on a raised
cosine, once every `BREATH_EVERY`. Only the color changes, so nothing
moves under the eye. A press releases the breath over `BREATH_RELEASE`
from the brightness drawn at that second. `next_frame` asks for every
frame of a motion, a breath, and a release, and between breaths it
names the second the next breath starts, which is a deadline on the
clock and not a read of state. Under the shade and under a film the
outline asks for no frame.

### How the browser holds it

The views stay stateless draws. A screen answers where its focus is at
rest, through `Screen::marked`, from the same geometry functions its
draw uses (`strip::focused`, `wall::focused`, `rail::focused`,
`people::focused` for a stripe's headshot, `buttons::button` for a
playback button, `watch::focused` for a mark on the status line, a
franchise row's `cell_box`, and the keyboard grid's cell). The strip
over every screen and the picker answer the same way. The browser
holds one `motion::Outline`, four `motion::Glide` values, and retargets
it on each press, after the screen took the press. It draws the outline as a layer of its own, over the
layer that holds focus and under every layer over that one, so the dim
of the room covers an outline on the page and the picker covers the
page's outline. A screen the outline follows draws no mark of its own.

Each screen holds its own scroll as a `Glide`, because only the screen
knows where its rows rest. The home page holds one for the page and one
for the row that holds focus, because every other strip keeps in view
the slot focus left on it and never scrolls on a press. A wall, a
person's wall of works, and a franchise's wall each hold one. A movie
page and a series page hold one for the page and a `motion::Rows`, one
`Glide` for each strip and stripe under the playback row or the
seasons. A row there scrolls back to the slot it keeps when focus
leaves it, so a press that moves focus from one row to the next eases
both. The header of a series page never scrolls, so a press between
the wall and the header's button row moves the outline and no scroll.
A franchise's wall moves only when focus leaves the view, so its next
rest is measured from the place its `Glide` last went to.
`Screen::follow` moves each scroll to where the screen now rests, on
the press's pace or at once, and the draw reads the eased value with
the frame's clock. A drawn value whose rest moved without a follow,
such as on a resize between a tick and its frame, draws at the rest.

The browser holds the pace of the last press (`motion::Pace`) and the
second the last motion ends (`motion::Breath`), which the breath and
`next_frame` run from.

### What does not move yet

The page backdrop's fade over the wall that opened it is not built.

## The curtain's line

The loading curtain names the work that starts under the mark where the
page's logo names another. A person who takes the up-next offer at the
credits ends the film before the request that names the next episode
reaches the browser, about 100 ms later, and the state that stood under
the film shows again in that gap. The line then named the episode that
had just ended for a frame or so. The state now clears its line when the
film plays (`Loading::played`), so the line names nothing until the
request lands. The line fades in over `DEPARTURE` from the second its
words were set, which is the press's own second for a press, so it
fades in with the mark, and it leaves with the mark.

## Later

- The film steps back when it is paused, in the same arrangement as the
  up-next step-back of plan 84.
- A poster that opens into the film at the start of a play. It is
  parked because the image pull and the film's start take a variable
  time, so the motion could end on a wait.

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
a wall scroll while decodes run, and what the frame budget is. Whether
the shade, the overlay that covers a sleeping screen, also fades in on
sleep and out on wake. `media-operator` decides that. A held key's rule
is decided above.
