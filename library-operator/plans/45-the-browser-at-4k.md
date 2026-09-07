# The browser at 4K

Plan 45. The media browser lays out in logical pixels and decodes
every piece of art at physical pixels, so a 4K panel at Weston's
scale 2 shows the 1080p layout with art at the panel's full
resolution. The same build stops the browser drawing under a film.

## The problem

The first house `Player` drove a 4K television, and two things showed.

The browser drew at half size. Display-operator plan 15 answers the
layout: Weston states `scale=2` on the output, and iced lays the
browser out in logical pixels. But every view asks the art store for
art at the logical size of its slot. At scale 2 a 270-wide poster slot
would get a 270-pixel decode stretched across 540 physical pixels,
and the whole point of a 4K panel is the art.

The browser drew sixty frames a second under every film. A `Play`
press enters a loading state whose mark pulses, and while that state
stands the browser asks for a frame on every pass. The state ends
only on the bus's `Present` or `Wake` moment, which arrives when the
`Play` ends. The present mode is `AutoNoVsync` on purpose, because a
FIFO surface blocks a covered client and it goes deaf. So for the
length of the film the browser rendered a full 3840x2160 surface that
nobody saw, and on an Alder Lake-N the film dropped half its frames.
The bus already delivers a `Status` moment with the unit's activity,
and the browser ignored it.

## The contract

- **Layout is logical, art is physical.** Every view keeps asking the
  store for art at the logical size of its slot. The store multiplies
  that size by the window's scale before it decodes, so the texture
  it hands the canvas is one texel per physical pixel. The canvas
  draws it into the logical bounds, and the result is 1:1 on the
  panel. The disk cache keys on the physical size, so a 1080p run and
  a 4K run of the same screen keep separate files.
- **An image reports its logical size.** A contain fit answers the
  size the decode landed at, and two views lay out from it. The image
  reports that size in logical pixels, so those views place a logo the
  same at either scale.
- **The scale follows the window.** The harness tells the screen the
  window's scale on every resize, the same place it builds the
  viewport. A `--scale FACTOR` flag overrides the window's answer, for
  a run on a machine whose compositor states 1. The pod never passes
  it. It is a test knob, and `local/browse --size 3840x2160 --scale 2`
  is the 4K panel on a laptop.
- **A film covers the browser.** On a `Status` moment whose activity
  is not `Idle`, the browser marks itself covered and its schedule
  answers no frame, as the asleep state does. `Present` and `Wake`
  clear the mark. The loading state stays where it is, so the
  `Present` at the end of the film still runs the return motion, and
  the frame after it is the page whole again.
- **The return waits for the surface.** A `Present` asks the harness
  for a fresh window, and the return motion starts on the frame the
  harness reports that window up, not at the `Present`. The return
  runs on the clock, and a compositor takes its own time to map a
  window, so a return started at the `Present` ran out before the
  first frame anyone saw. That is why the return was never seen on the
  house `Player`.
- **The budgets stay.** The art claim and the in-memory budget keep
  their sizes. Art at scale 2 carries four times the pixels, and the
  browser's memory on the 4K panel is read in the drill before either
  budget moves.

## What was considered

- **Every view multiplies by the scale itself.** Each of the store's
  callers could pass a physical size. There are a dozen callers, and
  every new view would have to remember. One multiplication in the
  store is the rule stated once.
- **A present mode that waits for frame callbacks.** FIFO would make
  the compositor pace the browser, and a covered surface gets no
  callbacks, so the browser would stop drawing by itself. Media plan
  20 measured that: a FIFO client under a film blocks in present and
  stops folding the bus, so a press arrives seconds late. Mailbox with
  a paced loop is the design, and the pace has to know when to stop.
- **A timeout on the loading state.** The pulse could run for a few
  seconds and then hold. That stops the waste without a bus change,
  but it guesses at how long a `Play` takes to cover the screen, and
  the `Status` moment already says so.

## The proof

Local, on vega, before any push:

1. `local/browse --size 3840x2160 --scale 2 --headless --capture`
   draws the home page with the 1080p layout and poster textures at
   twice the slot size. The captures at scale 1 and scale 2 lay out
   the same.
2. The unit tests state the store's multiplication, the image's
   logical size, and the covered schedule: a `Status` of `Playing`
   answers no frame, and `Present` answers one.

On the living room `Player`, on a dev build, with display plan 15:

3. The home page lays out as at 1080p and the art is sharp.
4. Start a film from the browser. The browser's CPU stays near its
   idle level for the length of the film, and mpv drops no frames.
5. Read the browser's memory at the end of the film and write it
   here.
