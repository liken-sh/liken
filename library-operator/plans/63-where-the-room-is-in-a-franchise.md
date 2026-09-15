# 63, Where the room is in a franchise

A franchise page shows a story in order, and the people in the room are
somewhere in that story. This plan puts that place on the page, puts a
bar of progress under every member, and makes a press on a split series
member open the series where the room is inside that member's run.
Designed 2026-09-14.

## The problem

**A split series opens at the wrong episode.** A franchise file cuts a
series into runs, so one show can stand at two places in the story.
The page opens every series member with the same call, which lands on
the next unwatched episode of the whole show. A press on the second
half of a show lands in its first half.

**The page shows no progress.** Every wall of films draws a bar under a
film the room has started. The franchise page draws none, for a film
or for a series member.

**The page opens at the top.** The room's place in the story is a
fact the home page already computes for its continue-watching card.
The franchise page ignores it, and focus opens on the first row.

## The design

### A press on a series member opens inside its run

`InFranchise`, which a page carries when it was opened as one member
of a franchise, gains the member's runs, as the catalog entry holds
them: (season, episode) pairs, where episode zero is a whole season,
and an empty list is the whole show.

The series page's next-episode walk cuts its leaves to the episodes
the runs cover, with `Entry::covers`. Focus lands on the next
unwatched episode inside the run, and on the run's first episode where
no play of the room names one. Where the catalog holds no episode of
the run, the cut is empty, and focus lands on the first still of the
show, which is what the page does today.

The audience is the people the browser holds as watching. The read
runs at every open and re-read, with that list, so the landing follows
who is in the room.

### A bar under every held member

A film row draws the bar the film walls draw: the share of the running
time the room's thread reached.

A series row draws the share of its run's episodes: an episode the
room finished counts whole, the episode the room is in the middle of
counts its fraction, and the total is the episodes the catalog holds
inside the run. Two halves of one show carry two bars. The count stands
in for a time-based share because the catalog holds no duration for
many episodes, and a count reads the same to a person.

The bar reuses `views::progress::bar` at the foot of the card's art.
A thin row draws no bar.

### A column of who is where

The lane gains one column, between the metro strip and the cards, the
width of one circle. It takes room only when a thread stands, the way
the time column takes room only when a row carries a time.

The leaves are the ones the home page builds for a franchise
container: the films and the covered episodes in story order, each
leaf tagged with its member's position. The thread walk over them is
the one every container uses.

The column draws two kinds of marker:

* **The room's stack.** One walk with the whole audience. Its offer
  names a leaf, the leaf names a member, the member names a row. The
  circles of everyone present draw on that row, side by side, with a
  small overlap.
* **A solo circle.** One walk per person, alone. A person whose own
  thread stands on a row other than the room's row draws one circle
  there. A person whose solo thread stands on the room's row draws
  nothing extra.

A circle is the initial in a circle that the audience screen draws for
a person. No profile pictures exist. A room of one person shows one
circle. A walk that offers nothing draws no marker.

### Focus opens on the room's row

On the first progress read after open, focus moves to the room's row
when the room's walk offers one. Later reads keep focus where it is,
guarded the way the series page guards with `placed`, so a play in
another room does not move focus while a person browses.

### The progress read

The franchise page joins the screens the browser's `read_progress`
reaches. One read hangs the bars, the markers, and the first-read
focus. It runs at every open and at every progress change.

The read costs one episodes read and one plays read per held series
member, and one plays read per film, then one walk per person and one
for the room over rows in memory. The home page pays the same for a
franchise card.

## The local drill

The design is iterated in `local/browse` with headless captures before
anything is pushed. `local/seed-progress` gains a franchise scenario:
it picks a franchise with a series member cut into two runs, and
seeds the room partway into the second run, one person alone on an
earlier film, and one person finished with the whole story. A capture
of the franchise page then shows the stack, a solo circle, no marker
for the finished person, the bars, and focus on the room's row.

## What this plan does not do

It does not thread a person across franchises, and it does not change
the thread rule. It does not draw the markers on the metro strip.
