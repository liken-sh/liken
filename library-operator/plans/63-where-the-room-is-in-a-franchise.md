# 63, Showing the viewers' place in a franchise

A franchise page shows a story in order. The people watching at the
screen, the viewers, have reached some point in that story. This plan
shows that point on the page and draws a progress bar under every
member. It also makes a selection of a split series member open the
series at the viewers' position inside that member's run. Designed
2026-09-14.

## The problem

**A split series opens at the wrong episode.** A franchise file splits a
series into runs, so one show can occur at two places in the story.
The page opens every series member with the same call, and that call
opens the next unwatched episode of the whole show. A selection of the
second half of a show opens an episode in its first half.

**The page shows no progress.** Every wall of films draws a bar under a
film that the viewers have started. The franchise page draws no bar,
for a film or for a series member.

**The page opens at the top.** The home page already computes the
viewers' place in the story for its continue-watching card. The
franchise page does not use it, and focus starts on the first row.

## The design

### Selecting a series member opens inside its run

`InFranchise` is the value a page has when it was opened as one member
of a franchise. It gets the member's runs, in the form the catalog
entry stores them: (season, episode) pairs, where episode zero is a
whole season, and an empty list is the whole show.

The series page's next-episode walk limits its leaves to the episodes
that the runs cover, with `Entry::covers`. A leaf is one item in the
ordered list that the walk reads. Focus goes to the next unwatched
episode inside the run. Where no play of the viewers names an episode
in the run, focus goes to the run's first episode. Where the catalog
has no episode of the run, the limited list is empty, and focus goes
to the first still of the show, which is what the page does today.

The audience is the list of people that the browser records as
watching. The read runs, with that list, at every open and every
re-read, so the starting episode changes when the viewers change.

### A bar under every held member

A held member is a member that a library holds. A film row draws the
same bar that the film walls draw: the share of the running time that
the viewers' thread reached. The thread is the viewers' position that
the thread rule in `media-browser/src/catalog/progress/thread.rs`
computes from their plays.

A series row draws the share of its run's episodes. An episode that the
viewers finished counts as one whole episode. The episode that the
viewers are in the middle of counts as its fraction. The total is the
number of episodes that the catalog holds inside the run. Two halves of
one show have two bars. The count is used in place of a time-based
share because the catalog has no duration for many episodes, and a
person reads a count the same way.

The bar reuses `views::progress::bar` at the foot of the card's art.
A thin row, which is an entry that no library holds, draws no bar.

### A column that shows each person's position

The lane gets one column, between the metro strip and the cards, with
the width of one circle. The column takes space only when a thread
exists, the same way the time column takes space only when a row has a
time.

The leaves are the ones the home page builds for a franchise
container: the films and the covered episodes in story order. Each
leaf is tagged with its member's position. The thread walk over them is
the same walk that every container uses.

The column draws two kinds of marker:

* **The viewers' stack.** One walk with the whole audience. The walk's
  offer names a leaf. That leaf gives the member, and the member gives
  the row. The circles of all the viewers draw on that row, side by
  side, with a small overlap.
* **A solo circle.** One walk for each person, with that person alone. A
  person whose own thread points to a row other than the viewers' row
  draws one circle on that row. A person whose solo thread points to
  the viewers' row draws nothing extra.

A circle is the same initial in a circle that the audience screen draws
for a person. No profile pictures exist. One viewer shows one circle. A
walk that offers nothing draws no marker.

### Focus starts on the viewers' row

On the first progress read after the page opens, focus moves to the
viewers' row when the viewers' walk offers one. Later reads keep focus
where it is. The page guards this the same way the series page guards
with `placed`, so a play by a different audience does not move focus
while a person browses.

### The progress read

The franchise page is added to the screens that the browser's
`read_progress` reaches. One read supplies the bars, the markers, and
the focus on the first read. It runs at every open and at every
progress change.

The read costs one episodes read and one plays read for each held
series member, and one plays read for each film. Then it runs one walk
for each person and one walk for the viewers over rows in memory. The
home page has the same cost for a franchise card.

## The local drill

The design is iterated in `local/browse` with headless captures before
anything is pushed. `local/seed-progress` gets a franchise scenario. It
picks a franchise with a series member split into two runs. It seeds
the viewers partway into the second run, one person alone on an
earlier film, and one person who finished the whole story. A capture
of the franchise page then shows the stack, a solo circle, no marker
for the person who finished, the bars, and focus on the viewers' row.

## What this plan does not do

It does not follow a person's thread across franchises, and it does not
change the thread rule. It does not draw the markers on the metro strip.
