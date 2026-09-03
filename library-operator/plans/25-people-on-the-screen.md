# People on the screen

Plan 25. It makes the credited people of a library, the cast, the
directors, and the writers, something a person can follow from one
title to the next: a stripe of headshots at the end of a title's page,
and a page of their own for each person.

## Builds on plan 30

[Plan 30](completed/30-facts-art-and-contributors.md) is done. Its
credits fact writes each person to `.contributors/` with ids under
every scheme and a headshot, links each credit to that entry in
`credits.yaml`, and the scanner derives the `contributors`,
`contributor_aliases`, and `credits` tables. This plan draws those
tables.

## The problem

Every movie and every series carries its cast with roles, its
directors, and its writers in the body the scanner reads from the
sidecar. Plan 22 draws them as text under the buttons. A name is a
dead end: select does nothing on it, and nothing on the screen answers
"what else is this person in." Plan 14 is about a different kind of
person, the one holding the remote, and this plan does not touch that.

## The stripes

The end of a movie's page and the end of a series' page hold up to two
stripes, "Crew" and then "Cast". A stripe draws only when the title
credits someone in it, so a title with no crew shows the cast alone.
Each slot is a headshot with the name under it, and a dimmer second
line for the part: the character an actor played, and "Director",
"Writer", or "Director, Writer" for the crew. The crew stripe holds
each person once, the directors first and then the writers the
directors do not already name. The cast is in billing order.

The stripes are the last rungs of each page's focus ladder. On the
movie page they follow the set strip, or the buttons where the movie
is in no set. On the series page they follow the last season of the
episode wall. Up and down move between stripes, left and right move
within one, and the page scrolls a stripe into view as it scrolls the
set strip today. The wall of episodes does not move, because the
stripes sit after it and cost the wall nothing.

The text credits under the buttons go away. The stripes are the credits.

Select on a slot opens the person's page. A slot whose person has no
entry in the store, which is a name the credits fact could not resolve,
draws the name alone and does nothing on select.

## The person's page

The page reads down: the headshot at the left, and beside it the name,
the born and died dates where the entry holds them, and the biography
where `biography.txt` is beside the entry, cut to a few lines. Under
that, a wall of every movie and series the person is credited in, in
descending release order, so the newest work is first and a work with
no release date is last.

Each slot of the wall is the title's poster with the title and its
year under it, and a second line for the parts, joined: "Director,
Writer", or "Director, as Tony Stark". A series is one slot, not one
per episode, because the credits fact writes a series' people at the
series and not at its episodes. The subtle mark that tells a series
from a movie is the year: a movie shows the year it was released, and
a series shows the year it first aired.

Select on a slot opens that title's page, which has stripes of its own,
so a person can walk from title to person to title without a floor.
Back climbs the stack one screen at a time. The stack has no bound
beyond what the browser already keeps for the set strip.

## One person across libraries

A person has one row per library, keyed by their directory, and the
aliases carry their ids. The page opens from one library's credit,
takes that row's ids, finds the same person in the namespace's other
libraries by any shared id, and lists all their credits as one wall.
The headshot, the dates, and the biography come from whichever
library's entry holds them, the opening library first.

## The reads

Every read is a query of the browser's local copy of the catalog, and
every path in a result is relative to its library's volume, as the art
paths are today.

- The stripes of one title: the `credits` rows of the item, each with
  the part, the role, the contributor's path, and whether the entry has
  a headshot, from `contributors` by path. Billing order within a part.
- One person: the `contributors` row by library and path, and the ids
  from `contributor_aliases` by the same key.
- The same person elsewhere: `contributor_aliases` rows in other
  libraries with a scheme and id the person's ids include, each
  resolving to a path in that library.
- The works of one person: for each of their rows, the `credits` rows
  by library and contributor, joined to `movies` or `series` by the
  library's kind for the title, the release date, and the art. Grouped
  by title, with the parts joined, and ordered by release date
  descending across all libraries.

## What it costs

The stripes read at most a few dozen rows per title. A person's wall
reads their credits, a few dozen at most, and one row per title. The
people tables cost the screen nothing until a page opens, so the
one-gigabyte screen holds nothing more than it does today.

## Proof

- The movie page and the series page each show both stripes for a
  title with crew and cast, and only the cast stripe for a title with
  no crew.
- Down from the last rung lands on the first stripe, and up from the
  first stripe returns to it.
- Select on a headshot opens the person's page, with the headshot, the
  name, the dates, and the wall in descending release order. Back
  returns to the title.
- A person credited in both libraries on `liken-1` shows one wall with
  both libraries' titles.
- The text credits are gone from both pages.

## What is not decided

- Whether a person's page should list episodes for a guest who is in
  one episode of a series. The credits fact would have to write
  episode credits first, one provider call per episode, and the
  library's series hold many thousands of episodes.
- Whether the crew of a long-running series, which a provider lists by
  the dozen, should be capped at the credits fact rather than drawn in
  full in a stripe that only scrolls.
