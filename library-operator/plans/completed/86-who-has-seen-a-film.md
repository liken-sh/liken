# 86, Who has seen a film

This plan, written on 2026-11-06, puts on every film poster how far the
people at the screen got in the film, and makes the reads behind the
franchise page, the genre wall, and the library wall faster. Plan 41
built the people at the screen, plan 63 built the bars and the circles
of a franchise page, and plan 72 built the clear.

Status on 2026-11-06: built. The drill on a physical screen has not
run: the measurements below are from a workstation over a copy of a
home catalog.

Status later on 2026-11-06: the card columns that "Measured and left"
names are built. `movies`, `series`, and `episodes` carry
`content_rating` and `tagline`, every writer of a body writes them from
the same body, and every read of the two fields reads the columns, with
the body only for a row whose columns are still NULL. The library
wall's open went from 14.7 ms to 8.4 ms and the genre wall's from 9.9 ms
to 5.8 ms, with the columns written. Of the 8.4 ms, the query is
2.2 ms, and the rest builds the cards and lays out the text of the page
on screen.

## The problem

When people at a screen choose a film together, they need to know who
has seen what. The walls drew a bar under a film only when every person
at the screen had a play of it, and the bar showed the newest play of
anyone, so the bar of a film one person finished and another started
showed whichever of them played last. The rows of the home page, the
strips of a title's page, and a person's page drew no bar at all.

The franchise page was slow to open. Its progress read asked the store
once for each held member, again for each person at the screen, and
once for each series row. Each of those asks ran the whole progress
statement, which took about 16 ms, so a franchise of 124 members took
seconds.

## The contract

**The bar.** Each person's share of a film is their furthest play of
it, alone or with anyone. A play past the finished mark counts as the
whole film. A clear, a play at position 0, takes back that person's
earlier plays, so only the plays after it count. The bar under a poster
shows the least of the shares of the people at the screen, and no bar
where one of them has no play. With one person at the screen, the bar is
their share. The furthest play is taken over the latest, because a
person who finished a film and then started it again has still seen
it.

**The faces.** While two or more people are at the screen, a face for
each of them who has a play of the film draws along the inside of the
foot of the poster. A person with no picture draws as their letter on a
disc. The disc is an image, because the renderer paints every path of a
layer under every image of that layer, and so a filled path would draw
under the poster.

**Where.** Every film poster: the walls, the rows of the home page
other than the continue-watching row, the set and franchise strips of a
movie's or a series' page, a person's page, and the franchise page. A
series poster draws neither the bar nor the faces. The continue-watching
row keeps the play it resumes. The franchise page keeps the room's
marker and the solo circles of plan 63, which say where each person
stands in the story order, series included. The faces say who played
each film.

**One read.** The browser reads every play that any person at the
screen is on in one statement, with the people of each play. The thread
rule, the episode marks, the bars, and the faces are rules over that
answer in `catalog/progress/seen.rs`. The statement finds the audience's
plays through `play_people_person`, and a test fails on a plan that
scans a table. It reads the credits marks only for a play past half of
its work, because no play before the half can pass the finished mark.

**A kept copy.** The source keeps the answer until either file changes,
checked against SQLite's `data_version` of the catalog and of the
progress store. A franchise page's per-person reads, and every wall and
strip read after the first, take the copy. A write by either agent
drops it on the next read, whether or not the update stream delivered
the write's event.

**The franchise reads.** `items`, the list of films and series that the
franchise reads join, holds only the works that the read's members
name. SQLite cannot move a join's condition into a UNION ALL on the
right side of a join, so a list of every item was copied into a
temporary table, bodies included, and indexed again on every read.

## Measurements

A workstation, over a copy of one screen's catalog and progress files
from a home cluster: 1,509 films, 206 series, 44 franchises, 3,635
plays, and five people. Each figure is the median of five reads after
one warm-up read.

| Read | Before | After |
|---|---|---|
| Franchise page progress, 124 members, 1 person | 2,760 ms | 3.7 ms |
| Franchise page progress, 124 members, 3 people | 7,499 ms | 8.5 ms |
| Franchise page progress, 51 members, 3 people | 2,995 ms | 6.2 ms |
| Franchise page progress, 37 members, 3 people | 3,287 ms | 5.5 ms |
| Franchise page open, 124 members | 19.4 ms | 3.0 ms |
| Franchise page open, 51 members | 20.6 ms | 3.8 ms |
| Library wall progress, 1,509 films | 25 to 31 ms | 0.3 ms |
| Genre wall progress | 25 to 31 ms | 0.2 ms |
| Home franchise row | 8.3 ms | 6.7 ms |

The "after" figures for progress are reads of the kept copy. The first
read after a write runs the statement: about 40 ms, after about 23 ms
to open the connection on a cold source.

## Measured and left

The library wall's own read takes about 15 ms for 1,509 films, and
`json_extract` of the rating and the tagline out of each body, about
2.4 KB each, is 12 ms of it. SQLite does not answer the select list
from an expression index, so the fix is for the scanner to write the two
fields as columns. That is a change to the catalog schema and to the
scanner, and this plan does not make it.

Walls and the franchise page read on the frame thread. The home page
reads on the reader thread. With the reads above, a wall's slowest read
is its own query.
