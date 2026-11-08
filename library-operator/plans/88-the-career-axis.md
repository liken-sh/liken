# 88, The career axis

This plan, written on 2026-11-08, gives a person's page in the media
browser a timeline of the person's credits, the career axis, over one
row of posters in release order. The design was chosen with the user
from the comps on the canvas at
https://claude.ai/artifact/NbZZXiD6bi128PXezcDnN5 (the boards `Axis`,
`Posthumous`, `NoBirth`, `Long`, `Few`, and `Dense`). Plan 23 holds
the motion rules that the page follows, and plan 86 holds the rule for
who has seen a film.

Status on 2026-11-08: proposed.

## The problem

A person's page draws every credit as a wall of posters, newest first,
six across. The wall answers "what is this person in", but not "when":
a person who reads the wall cannot see where a career started, where it
was busy, where it stopped, or which part of it the people at the
screen have seen. The card under each poster repeats the title that the
poster already shows, and the wall scrolls down, so a career of 200
credits is 34 rows that a remote walks one row at a time.

## The contract

The page has three parts from the top: the head, the career axis, and
one row of posters. Everything below is in `media-browser`, and the
operator does not change. The catalog already holds every fact that the
page reads: `contributors.born` and `contributors.died`, the release
date of each work, `credits.part` and `credits.role`, the episodes that
a library holds, and the plays in the progress store.

**The head.** The headshot is 160 by 240 at the top left. Beside it are
the name, a facts line, and the biography cut to 3 lines. The facts
line joins the dates, the credits, and what the audience has seen:
`born 1970 · 31 credits from 1985 to now · you've seen 9`, or
`1564 to 1616 · 9 credits from 1991 to 2021`. Credits with no release
date add `, and 3 undated`. "You've seen" counts the credits that every
person at the screen has seen, and the line leaves it out when nobody
is at the screen.

**Seen.** Each credit has one of three states for the people at the
screen: nobody has seen it, some of them have, or all of them have. A
person has seen a film when the finished rule of
`catalog/progress/watched.rs` counts one of their plays as finished,
which plan 86's reach reads as a share of 1. A person has seen a series
when they finished every episode that the library holds. With nobody at
the screen, no credit is seen. The dots, the bars, the facts line, and
the label all count by this rule.

**The axis.** The axis is a line the full width of the content region,
in a band about 150 pixels tall under the head.

- The axis starts at the first credit's year, or at the birth year
  when the person was born no more than 25 years before the first
  credit. It ends at the current year when the person has a credit in
  the last 2 years, and at the last credit's year otherwise. A span
  under 10 years widens to 10, centered on the credits, or widens at
  the end when the axis starts at birth.
- When the axis starts at birth, the left end is labeled "born" in
  italic. Otherwise the line fades in from transparent up to the first
  credit, because the career has no known start on the axis. A person
  who died before the axis starts, such as a playwright whose plays are
  filmed centuries later, has the faded end labeled "died 1616". A
  death inside the span is a tick labeled "died 1964, age 56", and the
  line after it is dimmer.
- Each decade has a tick labeled with its year. The ticks carry no
  ages, because the card under each poster gives the age.
- Each credit is a 12 pixel dot at its release year. Credits of one
  year stack upward, 15 pixels apart. A dot is an open ring when nobody
  at the screen has seen the credit, a disc filled on its left half
  when some of them have, and a full disc when all of them have. The
  ring is 2.5 pixels wide and opaque, so it reads from 3 meters.
- When any one year has more than 5 credits, the whole axis draws one
  bar per year instead, on one scale, with the tallest year as tall as
  the band. Each bar has an outline, a full fill at the bottom for the
  credits all of them have seen, and a lighter fill above that for the
  credits only some have seen. Each read decides the mode, and a change
  of mode is not animated.
- A series that is still running draws a 4 pixel muted bar from its
  first year to now, at the height of the first row of dots. The bar
  shows the show's run, not the person's time on it. A series is
  running when its newest episode in the library aired in the last 2
  years, because the catalog holds no status for a series.
- A 3 pixel brighter part of the line spans the years of the credits
  that the poster row shows at rest. It is computed from where the row
  comes to rest, not from the scroll as drawn, so it does not jump a
  year each time a poster crosses the edge of the row.

**The row.** One row of posters at the size of a wall poster, six
across, oldest at the left, and the undated credits after the dated
ones. Line 1 of each card is the character an actor played, or the job
for the crew ("Director", "Writer"). Line 2 is the year and the age at
release: `1986 · age 15`, `1996 · 380 years after death`, or `2003`
with no birth date. The poster shows the title, so the card does not.
The bar and the faces under a film's poster are the ones every film
poster draws.

**Focus.** The axis and the row are two focus rows, and the browser's
one focus outline marks both.

- On the axis, the outline goes around a column one year wide and of
  one height: from above a stack of 5 dots, or above the bars, down to
  the tick. So left and right move only its `x`. A label over the
  column gives the year and its counts, `2009 · 11 credits · 3 seen`,
  and is clamped inside the content region. The label shows only while
  the axis holds focus.
- Up from a poster moves focus to the axis at that poster's year, and
  only the outline moves. Up from the axis moves nothing, so the press
  reaches the browser's strip.
- Left and right on the axis step to the previous or next year with
  credits. The row's anchor becomes that year's first credit, and the
  row scrolls so that the anchor is at the left edge. A year of 11
  credits then shows its first 6.
- Down or enter on the axis moves focus to the anchor poster, and the
  row does not move. With no move on the axis, up and then down returns
  to the poster that focus left.
- Left and right on the row move one credit, and the row scrolls by the
  strip rule, `strip::offset`, which centers the focused poster. While a
  poster holds focus, its dot draws in the accent and keeps its shape,
  and the bright part of the line follows the row.
- At the first or last year, and at the first or last poster, the
  press moves nothing. While an undated credit holds focus, the axis
  marks no year.
- Back leaves the page from either row.
- One press moves the outline, the label, the row, and the bright part
  of the line together, on one `GLIDE` and the press's `Pace`, down to
  `HELD_FLOOR` under a held key.
- The page opens on the credit whose title page opened it. With no such
  title, it opens on the newest dated credit, with the row at its right
  end.
- A re-read keeps focus on the same work by its library and id. A
  change of the people at the screen redraws the seen marks and the
  facts line at once.

**When the axis is not drawn.** With fewer than 5 dated credits, or
dated credits that span less than 5 years, the page draws no axis. The
row moves up under the head and is the page's one focus row.

**Ages.** With no birth date, no card shows an age. When any credit
gives a negative age, the birth date is wrong for this person and no
card shows an age. Age 0 is real and shows as "age 0".

## What was set aside

- **The ages under the decade ticks** of the comps. A second line of
  small numbers is hard to read from a couch, and each card has the
  age.
- **A dashed line after a death.** A fine dash can disappear when the
  image scales on a television, and a dimmer solid line gives the same
  fact.
- **A green bar under the focused year** in place of the outline. The
  browser has one outline, which glides from the poster to the axis; a
  second focus mark would appear while the outline disappears, and the
  strips already use an underline for their current member.
- **The label while a poster holds focus.** It repeats line 2 of the
  card.

## The build

1. The model, with no window: the dates and ages of a life, the card
   lines, the span, the mode, the ticks and the marks of birth and
   death, the seen states, the facts line, and the focus and key rules,
   each tested as a table.
2. The read: the person's works in release order, the episodes held
   for each series credit, and the seen state of each credit for the
   people at the screen.
3. The geometry: where the axis, the column, the label, and each poster
   go, and the two rest rules of the row.
4. The draw and the motion: the axis in dots or bars, the row, and the
   glides that follow a press.
5. The sample catalog: invented people for each state, so a capture
   from the harness shows the page with no catalog.
6. The manual: the browser guide and the browser skill.

## Proof

The unit tests cover the model, the reads, the geometry, and the
motion. The harness captures the page from the sample catalog with a
poster in focus, with the axis in focus, in the bars mode, with no
birth date, and with few credits. The drill on a physical screen has
not run.
