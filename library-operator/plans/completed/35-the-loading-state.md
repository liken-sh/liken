# The loading state

Plan 35. A stub for a later agent to shape. Between select on a title
and the first frame of the film, the page steps back and the title's
own art holds the screen with the mark beneath it.

## The problem

Select on a title starts a chain the browser does not control: the
request crosses the bus, the operator creates the `Play`, the playback
pod starts, and the film's first frame covers the surface. Plan 08
measured that gap at one to five seconds. The gap is the playback pod's
own start and this plan does not shorten it.

What a person sees in the gap is the problem. The page stays exactly as
it was, so the press looks lost, and the film then cuts in over a page
full of text. Plan 08 decided that the browser "receives nothing and
changes nothing" while the `Play` runs. That rule was about input. It
said nothing about what the browser draws while it waits.

The material is already on the page. A movie page and a series page
draw a backdrop and a logo, and plan 22 keeps their decoded handles in
the poster store. The `media-screen` crate the browser links already
reads the `Player`'s activity from the bus, and it names `Starting` and
`Playing`. `brand`'s Iced crate draws the mark, fourteen hexagons whose
swing follows one energy value, and `media-operator`'s idle screen
already raises that energy while a `Play` starts.

## The shape

- Select on a title publishes the request as it does today, and in the
  same frame the page enters its loading state. The state is a pure
  function of the clock, in the form plan 23 gives every motion, so the
  loop asks for frames while it runs and for none after.
- Every element but the backdrop and the logo leaves: the text, the
  covers, the set strip, the episode list, the header, and the focus
  outline. They fade or slide away in one short motion.
- The logo moves from where it sits to the centre of the screen and
  scales to a fixed share of the width. The backdrop stays, and its
  shade lifts so the art shows through as the page's own image.
- Beneath the logo, the mark appears and pulses at full energy, as it
  does on the idle screen while a `Play` starts. The mark comes from
  `brand`, not from a copy. The logo and the mark hold until the film
  covers them.
- A title with no logo centres its name in the title face instead. A
  title with no backdrop holds the wall's colour. The loading state
  never waits on a decode; where the art is not there, the state runs
  with what it has.
- The state ends in one of two ways. When the `Player` reaches
  `Playing`, the film covers the surface and the browser holds the
  loading state under it, so the surface never shows the page during
  the film. When the `Play` fails or the bus reports `Idle` again
  without `Playing`, the page returns whole in the reverse motion,
  and the person is where they were.
- The return is the same state run backward, and fast. When the film
  ends and `present` arrives, the surface first shows the backdrop,
  the logo, and the mark at full energy, as the idle screen does on its
  own arrival. The elements then return in reverse, quicker than they
  left, and the mark's energy eases to zero. The page is whole at the
  same focus, as plan 08 promises today.

## Decided

Built and released in 2026.09.03-009. The state has no ceiling. The
press enters it: a select on Play or on a still publishes the request
and the page enters the state in the same frame, and it waits on no
signal. The film covers the state, and either `Wake` or `Present`
starts the exit, whether the film played through or the `Play` never
started. A back press during the state exits it at once and cancels
nothing; the `Play` is the operator's to run. A series page enters the
state for an episode as a movie page does, with the series' logo and
backdrop. The departure takes 0.35 s and the return 0.12 s, in
`look.rs`. Plan 23 is still open; this state is the browser's first
clock-driven drawing, and the volume row is its second.
