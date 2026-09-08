# Threads

Plan 52. The continue-watching row answers "where did we leave off" for
the people at the screen, across series, sets, and franchises, with the
reason on every card. It needs no resource beyond the `Play`.

## The problem

A household watches in groups that change by the night, and the same
people come back to a series weeks later. Plan 14 meant a `Watch` to
hold that group, and plan 51 dropped it: the people at the screen name
their group every time they play, so the plays already say who shares
what.

The row today reads the plays of the people at the screen and offers
the next leaf in the work, the set, and every franchise, but three
things are wrong with it:

- **The audience rule shows the wrong threads.** A play counts today
  when every person at the screen is on it, so a person alone sees the
  threads of every group they are in. At bedtime alone, a parent sees
  the family's rewatch and skips past it every night.
- **A guest night stalls the thread.** If the rule were the exact set of
  people, one night with a guest would record a play the group never
  sees again, and their thread would offer that episode a second time.
- **The cards do not say why they are there.** The film after Iron Man 2
  in its set is Iron Man 3, and the next film in the MCU is The
  Incredible Hulk. Both belong in the row, and a card that does not say
  which is which is a guess.

## The contract

### A container is an ordered list of leaves

A leaf is an episode or a movie. Three kinds of container order them:

- **A series** lists its episodes in aired order, by season and episode
  number.
- **A set** lists its films in release order, tie-broken by sort key,
  which is the order the movie page's strip draws today.
- **A franchise** lists its members in story order. A member that is a
  movie is one leaf. A member that is a series expands in place into
  its episodes in aired order, cut to the seasons and episode ranges of
  its runs where the file names any. A member the namespace does not
  hold has no leaves.

A leaf the catalog does not hold is not in the list. A series that is
absent from every library contributes nothing to a franchise, so a gap
in the collection never blocks the walk.

### The thread rule

For the people at the screen and one container, the plays of the
container's leaves are read in two classes:

- **Own plays** name exactly the people at the screen.
- **Superset plays** name all of them and at least one more.

A play that names fewer than the people at the screen never counts,
because the group would not go on without one of its own. An empty
audience has no row; the pages still read the plays that name nobody.

The thread stands where the latest own play stands: on that leaf,
unfinished or finished. Then every superset play recorded after it is
taken in recorded order, and one counts only when its leaf is where the
thread expects the next play: the same leaf while the thread's leaf is
unfinished, or the leaf after it once it is finished. A superset play
that counts moves the thread to its leaf; one that does not is skipped
and the walk goes on. A container with no own play has no thread.

The rule in three sentences: a thread is yours when you started it.
Once it is yours, a night that had all of you counts when it continued
the thread. An old run of your own does not make someone else's new run
yours, because their first leaf is not the successor of your last.

### Next up is the plain successor

A thread whose leaf is unfinished resumes it. A thread whose leaf is
finished offers the leaf after it in the container's order, and offers
nothing when the leaf was the last. The rule never skips a leaf the
audience finished before: after Iron Man 2, the MCU offers The
Incredible Hulk even if the audience saw it years ago, because "after
what we last watched" is predictable and "the first one we have not
seen" is not.

Whether a leaf is finished is the store's rule today: the position
passed ninety percent of the duration.

### The cards

The row holds one card per leaf the containers offer. Each container
that offers a leaf puts a reason on the card:

- **Resume** for a thread whose leaf is unfinished. A series thread
  says "Resume · The Office".
- **Next in the series**, spelled with the series title.
- **Next in the set**, spelled with the set title.
- **Next in the franchise**, spelled with the franchise title.

A leaf two containers offer is one card with both reasons, in this
order: resume, series, set, franchise. WandaVision S01E04 after S01E03
reads "Next in WandaVision · Next in the MCU".

A press goes where the first reason points. Resume, series, and set
cards open the leaf's own page: the movie page, or the series page with
that episode focused. A card whose only reasons are franchises opens the
franchise page with that member's card selected, the way the series
page opens on an episode. The franchise page gains that entry point.

The row sorts by the recorded time of the play that moved each thread
last, newest first, and a leaf offered by two threads takes the newer
time. A card leaves the row when the audience plays past it or finishes
the container. Nothing else removes one, so an old thread sinks to the
tail and stays.

The row keeps its cap of twenty-four cards.

The row's heading is "Continue watching" followed by the circles of the
people at the screen, drawn the way the top strip draws them. The
circles are a rung of the row: left from the first card and up from
any card land on them, up again goes on to the row above, down returns
to the card focus left, and enter raises the person picker with the
room already chosen. With
nobody at the screen, the home page draws no row at all, neither
heading nor cards, and draws it again as soon as the picker is
answered. Nobody watching is the browser's incognito mode.

A card of an episode spells the episode on its first line, `E06 ·
Delusion`, and the season after the reasons on its second, `Next in
Wilfred (US) · S03`. Two digits for both, as the home page's stills
spell them. A film's card keeps its tagline or title on the first line.

### The marks

The bars under wall cards and the marks on a series page answer a
different question, "have we seen this", and follow a different rule. A
leaf is marked for the people at the screen when every one of them has
a play of it, in any group. The family's plays mark The Office's
episodes for each of them alone, and the thread stays the family's.

### The reads

The browser reads the store's rows and walks the containers itself. The
store's read gains one fact per play, whether the play names exactly the
people at the screen, and drops nothing else. The walk is one function
over an ordered list of leaves and the plays on them, and every
container kind calls it. The series page's "open on the next episode"
uses the same function, so a page and a card never disagree.

## What was set aside

- **A `Watch` resource.** Plan 51 says why.
- **The exact set of people alone.** A guest night would stall the
  thread; the superset rule above is the fix, and the successor test is
  what keeps an old solo run from adopting a new group's thread.
- **Skipping leaves the audience finished.** Predictable beats clever.
  The marks say what was seen.
- **One card per reason.** Two cards for one episode, one press to the
  series page and one to the franchise page, was honest but doubled the
  row. The stacked reasons say the same thing on one card.
- **A container for a season alone.** A season is a run inside a
  series and needs no thread of its own. A franchise names one through
  its runs.

## The proof

Five nights with three people A, B, and C on a series S, each a test
case of the walk:

1. A, B, C watch E1 and E2. A, B, C see "next E3". A and B see nothing.
   A alone sees nothing.
2. Then A, B, C, and D watch E3. A, B, C see "next E4". A, B, C, D see
   "next E4". A and B see nothing.
3. Instead, A and B watch E3 without C. A, B, C still see "next E3".
   A and B see "next E4".
4. A watches E1 alone, then A and B watch E2. A sees "next E3". A and B
   see "next E3". B sees nothing.
5. A watched all of S alone long ago. A, B, C watch E1. A alone sees
   nothing of S. A, B, C see "next E2".

Then the same shape across a set and a franchise: a film after a film,
a film after a series member's last held episode, and a series member
cut to a run. Then the collapse: an episode a series and a franchise
both offer is one card with two reasons, and its press opens the series
page on that episode; a film only a franchise offers opens the franchise
page on that member.

The drill runs on the house against the backfilled store: a person
alone sees none of a group's threads, and a group sees its own.
