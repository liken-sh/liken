# The rail and the strip

Plan 40. A right-hand rail on long walls that jumps through the list
and cycles its order, a seasons rail on long series pages, a search
icon by the clock on every screen, and the genre head folded into the
band.

## The problem

Plan 39 built a filter control and a pick of genres and decades. In
use, the pick is a wall of buttons over a wall of posters, and it
answers a question people do not ask: search reaches a person or a
title in a few letters, and a genre wall is one press from the home
page. What people do ask is "get me to the 1990s" or "get me to season
seven" in a long list, and the pick does not do that. The genre wall
also draws a head over its first row that persists as the wall scrolls,
with no scrim, and its band says only "Genre".

## The contract

### The rail

A wall that is longer than four screens draws a rail at its right
edge. The rail is the `views::rail` primitive the franchise page draws
at its left, which stays where it is: the franchise rail is story
time, and this rail is the list's own order.

The rail always fits the screen. Every bar is on screen at once, in
equal shares of the wall's height, and the scroll never moves it. The
sort button is over the bars, on the lighter ground so it reads as a
control, with a gap under it. The bars are the finest unit of the
wall's order whose labels fit: a bar has to be as long as its longest
label, so the label length decides how many bars fit.

- In release order, years if the wall's years fit, otherwise decades,
  otherwise ranges of decades.
- In title order, single letters if they fit, otherwise ranges such as
  "A–B".
- In a genre wall's leading order, two bars: "primarily Science
  Fiction" and "other Science Fiction".
- A unit with fewer than one row of titles merges into its neighbor,
  so no bar jumps to nothing.

A bar's label reads top to bottom, centered in the bar, on a dark fill
that reads over any art.

Right from the last column enters the rail at the bar that covers the
focused row. Up and down move along the bars and onto the button, and
the wall scrolls to follow the bar. Enter on a bar puts focus on the
first title the bar covers, which is mid-row where a letter starts
mid-row. Left returns to the wall. Escape is back, as everywhere.

### The order

The sort button cycles the wall's order, and the order names the
rail's bars. There are four orders and no others:

- **Genre**: the titles that carry the genre first, then the rest,
  each run newest first. Only a genre wall has it, and it is that
  wall's first order. The code calls it `Leads`.
- **Newest**: release date descending.
- **Oldest**: release date ascending.
- **Title**: alphabetical by sort key, "The Matrix" under M.

A library wall cycles Title, Newest, Oldest. A genre wall cycles
Leads, Newest, Oldest, Title. The recency walls are newest first by
definition and draw bars with no button. A person's page, a set, a
franchise, and a search draw no rail.

The sidecar formats the order into the reads it makes today, on the
`(library, sort_key)` and `(library, released)` indexes.

### The seasons rail

A series page with more than four seasons draws the same rail at the
right of its still wall, one bar per season, labeled by the season's
number. Where more seasons exist than bars fit, neighboring seasons
merge into ranges, "1–3". Enter on a bar puts focus on the season's
first still. The series page also gains its genres on the facts line,
the way a movie's page has them.

### The strip

The clock's layer becomes a strip the browser draws over every screen:
a magnifying glass at the clock's left, and the clock. On a search
wall the glass expands into the text field, and the wall's own band
keeps its heading, "adam sc · 18", at the left.

An up press that moves nothing on any screen puts focus on the glass.
Enter on it opens the search wall with the keyboard grid shown. This is
how a remote with only arrows reaches search from a movie's page. The
search key and a typed letter still open search from anywhere.

The band's controls are gone. The band draws the heading alone.

### The head

The head over a genre wall is gone. The band carries what the head
did: "Science Fiction · 429 movies, 70 series", counted by kind off the
answered items.

### The index

A person is one entry across libraries. The read that builds the search
index folds contributor rows with the same path into one person, with
a headshot if any of the rows has one.

## What is set aside

- **The filter control and the pick**, from plan 39. Search and the
  rail answer what they answered, and the pick was a wall over a wall.
- **Filters and facets in the catalog reads.** No consumer is left,
  and a facet read per wall open was a query nobody looked at.
- **Every other sort and filter.** Four orders is the whole set. A
  library that offers every sort under the sun makes every list feel
  the same, and nobody uses them.
- **A scrim under the head.** The head is gone instead.
- **Decade bars inside the two run bars of the leading order.** The
  rail draws lanes, so a second lane can carry them if the two bars
  alone are not enough.
- **The whole label on focus.** Labels are numbers, letters, and
  decades now, and none trims, so nothing needs to show more on focus.
- **Single letters where a pair merges.** A merged pair reads "A–B",
  and the range label is what caps the count at 14 on a large library.
  Labeling a pair by its first letter alone would give about 20 bars.
- **The leading run's boundary is read off the release dates**, where
  the second run's newest title starts. The rank does not reach the
  browser. It is wrong only when the second run's newest title is
  older than the first run's oldest.

## Proof

- On a local run, a library wall draws the rail with letters, and the
  button cycles to Newest and the bars become decades or years.
- A genre wall opens in its leading order with two bars, and enter on
  "other Science Fiction" lands on the first title that does not lead
  with the genre.
- A series with more than four seasons draws season bars, and enter on
  one lands on its first still.
- Up from the top of a movie's page focuses the glass, and enter opens
  search with the grid.
- A person who is in two libraries appears once in search.
- The genre band reads "Science Fiction · 429 movies, 70 series" and no
  head draws over the first row.

## The drill, 2026-09-06

Built and drilled the same day on this workstation from `local/browse`.
The library wall draws 14 letter bars and its button cycles to Newest,
where the bars are decades. A genre wall opens in its "Genre" order
with two bars, and enter on "other Science Fiction" lands on the first
title that does not lead with it, mid-row. A 28-season series draws
seven merged bars and a 12-season one draws twelve, numbered, on dark
pills that read over the art. Up from the top of a movie's page focuses
the glass, and enter opens search with the grid. The genre band reads
"Science Fiction · 429 movies, 70 series".

Five things the drill changed. Bars are sized by their longest label,
not a fixed slot, and fitted against nine tenths of the height so a
window a little under 1080 still reads. Labels read top to bottom and
sit centred in their bars. The button draws on the lighter ground with a
gap under it. The season divider reads "Season 1 · 1999 · 9 episodes".
The facts line on movie and series pages ellipsizes a long list of
genres instead of ending on a comma.

The `liken-1` drill is still owed.
