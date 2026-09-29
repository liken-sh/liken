# 73, Pick up here

Built on 2026-09-29. The drill on a cluster with Jellyfin is still
owed. This plan answers the open problem "A series has no progress of
its own" for its middle case: a person says "watched up to here" from
an episode's row and plays that episode in the same press. The open
problem is closed, and its two other items are under "Not in this
plan".

Built: the Pick up here button and its glyph, the list form of the mark
on the bus, the progress role's rows for a list, the jellyfin role's
writes for a list and its skip of an item already played, and the
arrival on an episode's row from a home card, which "Arrival on an
episode's row" below describes. Not built: the drill under "How it is
proved".

## The problem

A person who watched the first seasons of a series somewhere else, or
before the cluster kept progress, starts the series here in the middle.
The browser does not know they watched the earlier episodes. The series
page draws those episodes as not started, and Continue watching follows
whatever the person plays next with no idea of the episodes before it.
The one way to fix that today is "Mark watched" on each earlier
episode's row (plan 72): open the row, move down to the mark, press it,
and do that again for every episode. On a series of 150 episodes that
is not a real option.

## The design

### The button

An episode's row on the series page gets a button, **Pick up here**,
after Play, or after Resume and Start over. A press does two things:

1. It marks every earlier episode of the series watched, for the people
   at the screen.
2. It starts this episode, the same way Play does: from the beginning,
   with the same next episode after it.

"Earlier" means every episode before this one in series order, across
seasons, from the first episode of the first season. The catalog orders
episodes by season and then by episode, so the order is the numbers'
order. Specials, season 0, stand outside that order, and the button
leaves them alone. A special's own row does not show the button.

The press marks an earlier episode that the audience has not finished,
including one they are in the middle of. It skips an episode they
finished, because the mark would not change what the page draws. It
also skips an episode with no duration, the same way a single watched
mark does: the store reads a position as finished only against a
duration.

The button shows only when the press marks at least one episode. On the
first episode, or when every earlier episode is already finished, the
press would do no more than Play, so the row does not show it.

### Its icon

A vertical bar, then a triangle that points right, away from the bar:
"from here onward". It is Start over's glyph with the triangle turned
round: Start over's triangle points left, at the bar. The bar and the
triangle keep Start over's sizes, and the glyph draws with the same
stroke, caps, and joins as every other glyph in `views/icon.rs`.

### One message for the whole press

The press publishes one mark that names every episode it covers, on the
same topic and in the same retained form as a single mark. The payload
adds a list, `episodes`, and each entry states one episode's `season`,
`episode`, `position`, and `duration`. The series' aliases, the people,
the `Player`, and `at` are stated once, for the whole list.

The progress role writes one row for each entry, named
`{name}-s{season}e{episode}` with each number in four digits, all
recorded at `at`. The jellyfin role writes each entry to each person at
the screen. The sent record stays one record for the whole mark.

Each guarantee of the mark in plan 72 holds for the list, entry by
entry:

- **A progress role that is down.** The list is one retained message,
  and the broker delivers it when the role subscribes again, as it does
  a single mark.
- **A redelivery never overwrites a newer row.** Each entry's row is
  written only where the store holds no row of that name recorded at
  `at` or later, the rule `recordOutside` applies to every mark. A role
  that fails halfway leaves the message on the broker, and the next
  delivery writes the rows that are missing and leaves the rest.
- **Retained marks do not pile up.** One press leaves one retained
  mark and, where Jellyfin is in use, one sent record. The progress
  role clears both 24 hours after `at`, as it does today.
- **An old mark never overwrites newer state in Jellyfin.** The role
  reads each person's user data for each entry's item before it writes,
  and leaves an item played at or after `at`. The sent record covers
  the whole list, so a restarted role sends none of it again. A pass
  that fails partway leaves the mark for the next pass, and on that
  pass the items it already wrote read back the mark's own date and
  are left alone.

The single mark is the list's form for one work, and both forms read the
same fields, so the progress role and the jellyfin role have one code
path each, looping over one entry or many.

The rows' names order them. The thread rule breaks a tie in recorded
time on the play's name, and every row of one press has the same `at`.
Four-digit numbers make the names sort in series order, so the thread
stands on the last earlier episode, which is finished, and offers the
episode the person picked up. The play of that episode is recorded after
the press and takes the thread from there.

### Fewer writes to Jellyfin

Jellyfin has no endpoint that writes several items' user data at once,
so a list of 149 episodes is still up to 149 reads and 149 writes for
each person. The jellyfin role writes no item that Jellyfin already
holds as played with no resume position, because a watched mark would
change nothing there but the date. A person who watched the earlier
seasons in Jellyfin costs reads and no writes. The role writes one log
line for the whole list for each person, with the counts, and not a
line for each episode.

### Arrival on an episode's row

A home card that names one episode opens the series page on that
episode's still today, and the person presses enter to open the row and
enter again to play. The continue-watching row, the recently added row,
and the banner all name episodes this way. So a card that names one
episode opens the page with that episode's row already open, and focus
on its first button, Resume or Play. A card that names a series and no
episode opens the page on its wall, as before.

Back from that row returns to the home page in one press, because the
person never saw the wall. The page records that its open row is the
one it opened on. Once focus leaves the row for the wall, back closes a
row as it does on any series page. A re-read of the page, when the
store or the catalog changes under it, keeps the record.

The rule applies to the home page alone. A search hit or a wall of
episodes opens the page on the still, as before, because the person
chose the episode from a wall.

## Set aside

**One mark for each episode.** Each episode would be a mark of its own,
on its own topic, and each guarantee would hold with no change to
either role. It fails on three counts:

- A mark's name is `mark-{player}-{at}`, one name for each second, and
  the browser moves a second press within the same second to the next
  second. 149 marks from one press would take 149 seconds, so the last
  of them would carry an `at` nearly two and a half minutes in the
  future. The play of the picked episode would be older than those
  marks for that long, and Continue watching would stand on a mark and
  not on the play.
- One press would leave up to 149 retained marks, and 149 sent records
  beside them, for 24 hours.
- The jellyfin role would send and record each mark on its own, so a
  pass that fails partway would leave some episodes sent and others
  not, with nothing that ties them to one press.

**A mark on the series, and not on its episodes.** The store would hold
one row that says "watched up to season 3, episode 4", and every reader
would expand it. The thread rule, the episode progress read, and the
jellyfin role would each need a new rule for a row that stands for
many episodes, and the rows would no longer say what they hold.

## Not in this plan

These two items of the open problem stay open:

- **Clearing a whole series**, to take it off Continue watching or to
  start it again from the first episode. The list form carries a
  cleared mark as well as a watched one, so the bus needs no change,
  but the page needs a place for the button and a rule for which
  episodes it clears.
- **The series' place in one view**, such as how far through the series
  the audience is, drawn on the series page or on its card.

Also left out:

- A "Pick up here" on the Continue watching card or on a franchise's
  page. The series page is where a person sees the episode they want.
- Marking the earlier episodes for one person out of several at the
  screen, the same limit plan 72 has.

## What the design must answer

- Whether Pick up here resumes an episode the audience is in the middle
  of, or starts it from the beginning.

  Answered: from the beginning, the same play Play asks for, with the
  same next episode after it. Resume stays the button that starts from
  the point the audience reached.
- Where the row puts the button when the episode offers Resume and
  Start over.

  Answered: last, after Start over, so the first button of the row is
  always the one that plays the episode as it stands.

## How it is proved

- Browser tests: the button shows on an episode with an earlier
  unfinished episode and hides on the first episode, on a special, and
  where every earlier episode is finished; a press marks the earlier
  episodes across seasons, skips specials, finished episodes, and
  episodes with no duration, and marks a partly watched one; the press
  asks for the same play as Play; the browser publishes one retained
  list mark and then the play request.
- Progress role tests: a list mark writes one row for each entry, named
  in series order and recorded at `at`; a list delivered again after a
  forget changes no row; a list whose write failed halfway writes the
  rest on the next delivery.
- Jellyfin role tests, against the fake Jellyfin server: a list mark
  writes each episode for each person, skips an episode Jellyfin
  already holds as played, leaves one sent record, and a pass that
  failed partway writes only the rest on the next pass.
- Browser tests for the arrival: a continue-watching card for an
  episode the audience stopped inside opens on the row with focus on
  Resume, one they have not started on Play, a recently added episode
  and a banner title on the row, and a series card on the wall; back
  from the row returns to the home page, and back from a row opened
  again from the wall closes the row.
- A drill on a cluster with Jellyfin, still owed: on a series with two seasons
  watched nowhere, press Pick up here on the first episode of the third
  season; see the earlier episodes drawn watched on a second screen,
  played in Jellyfin, and the picked episode on Continue watching after
  the play stops.
