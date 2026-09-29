# 72, Marking a title watched or cleared

Designed, not built. A person at the screen can mark a title watched
or clear its progress from the browser, and the Jellyfin sync carries
both marks in each direction without a watched flag of its own.

Half built on 2026-09-29. The two marks and the rule for a play at zero
are built: the browser's thread rule, the two buttons on a movie's page
and on an episode's row, the mark message on the bus, and the progress
role's record of it. The Jellyfin half is not built: the outbound
writer sends no mark, a rewatch still sends `Played: false`, the
finished line still differs between the two sides, and the jellyfin
role runs no reconcile at start. "The mark as built" below states the
contract the Jellyfin half builds on.

## The problem

"Continue watching" holds every title that the people at the screen
started and did not finish. A person cannot take a title off the row.
A film abandoned after ten minutes stays there until 24 newer plays push
it past the cap (`catalog/recency.rs`). A film finished in another room
without the cluster stays there too. Nothing in the cluster writes
progress except a play: the browser only reads the store, the
`kubectl liken library` plugin has no progress verb, and no resource
has a progress field.

A person with Jellyfin can toggle an item played or unplayed there, and
the webhook carries the toggle in. That route has its own faults,
listed under "The Jellyfin sync" below, and library-operator must work
with no Jellyfin at all.

## The design

### Two marks, and no watched flag

The store needs no new column. A title is finished when its newest play
reaches the finished line, and the reader computes that from `position`
and `duration` (`media-browser/src/catalog/progress.rs`). So the two
marks are two positions:

- **Mark watched** writes a play at `position = duration`. The title
  is finished. On a film, the card leaves the row. On a series or a
  franchise, the thread moves on, and the next leaf takes the card, the
  same as when the person watches to the end.
- **Clear** writes a play at `position = 0`. The title leaves the row,
  and the whole container leaves with it: a cleared episode does not
  offer the next one.

Each mark is an ordinary row in `plays`, with the people at the screen
in `play_people` and the title's provider ids in `play_aliases`. It is
the newest row for that title, so it stands the thread the same way a
play does. A later play replaces it the same way.

### A play at zero is not started

Today a row at position 0 makes a Resume card at 0:00. Clear needs the
opposite, and so does an unplayed toggle from Jellyfin, which arrives at
position 0. The thread rule (`catalog/progress/thread.rs`) changes: an
unfinished standing leaf at position 0 makes no card and offers no next
leaf. The container drops out of the row.

A "Start over" play also begins at 0 and moves on within seconds. A
person who starts over and stops before the first position report gets
no card. That is the same result as not starting.

### Who a mark applies to

A mark applies to everyone at the screen, the same audience the row is
computed for. The row only shows threads whose standing play names
exactly those people, so a mark with that audience is what removes the
card they see. A person who watched alone keeps their own thread.

### Where a person marks

The Continue watching card and the title's page each get the two
actions. On a card, the actions apply to the leaf the card names: the
film, or the episode it would resume. On a series page, they apply to
the episode in focus. A mark on a title with no play yet takes its
duration from the catalog (`query.rs`, `duration`).

The browser does not write the store. It publishes the mark on the bus,
and the progress role writes the row, the same way it writes a play
that ran outside the cluster (`recordOutside` in `progressstore.go`,
`outsidePlay` in `progressbus.go`). The browser reports intent, and
library-operator owns the store.

### The Jellyfin sync

Jellyfin is an add-on. Both marks work with no `spec.jellyfin`, and
every rule above holds without it. With it, each side's watched state
maps onto the other through positions:

| Here | Jellyfin |
|---|---|
| A play past the finished line, or mark watched | `Played: true`, position at the end |
| Clear | `Played: false`, position 0 |
| A play stopped before the line | `PlaybackPositionTicks` at the position |

The sync has faults that a mark exposes. This plan fixes these:

- **Marks are never pushed.** The outbound writer sends only live
  Plays (`jellyfinoutbound.go`). A mark is a row with no Play, so it
  never reaches Jellyfin. The writer sends each mark once, when the
  progress role records it.
- **A rewatch clears Jellyfin's mark.** Each position report of a
  replay sends `Played: false` until the new position passes the line.
  A person who rewatches ten minutes of a finished film leaves it
  unplayed in Jellyfin. The writer sends `Played: true` when a play
  crosses the line and never sends `Played: false` for a play. Only a
  clear sends it.
- **An unplayed toggle puts the title on the row.** It arrives at
  position 0, and the rule for a play at zero fixes it.
- **The two sides disagree about the credits.** The outbound writer's
  finished line moves to the start of the credits (`creditsLine` in
  `watched.go`). The browser's line does not. A film stopped in its
  credits is played in Jellyfin and offers Resume here. One rule must
  decide both.
- **A missed event is lost.** The webhook's messages are not retained,
  and the backfill runs once for each server URL. A toggle made while
  the jellyfin role or the progress role is down never arrives. The
  webhook is the subscription, so the rule in `AGENTS.md` applies: each
  time the jellyfin role starts and its webhook listener is up, it reads
  every user's played and resumable items once and records what
  changed. The newer time wins, as it does in `recordOutside` today.

### The mark as built

The browser publishes each mark retained on
`plays/{namespace}/mark-{player}-{at}/mark`, one topic for each press,
so a second mark cannot replace a first that no reader has recorded.
The operator names the branch `{base}/plays/{namespace}` on the
browser container as `LIBRARY_PLAYS_TOPIC`, and the browser reads the
`Player`'s name from `MEDIA_PLAYER_NAME`. Two presses on one screen in
the same second take the next second, so each name is unique and the
later press has the later `at`.

The payload is `titleMark` in `progressbus.go` and `bus/mark.rs` in the
browser: `mark` (`watched` or `cleared`), `player`, `people`,
`aliases`, `season`, `episode`, `position` (the duration for watched,
0 for cleared), `duration`, and `at`, the Unix second of the press.

The progress role records a mark through `recordOutside`, as an ended
row named after the mark, with `recorded = at`. A row of that name
recorded at `at` or later is left alone, so a redelivery never changes
a row, and never writes back a person that a forget removed. The role
clears the retained topic 24 hours after `at`, once it has recorded
the mark: at once for a mark older than that, and with a timer for a
newer one. A role that restarts before the timer fires reads the mark
again and schedules the clear again from the same `at`. A payload that
is not a mark is cleared at once.

The jellyfin role subscribes to `plays/{namespace}/+/mark`. It
receives each mark live, and each mark younger than 24 hours again on
every subscription. So the jellyfin role reads a mark that was
published while it was down, but it must treat a second delivery as
the same mark, and must not let an old mark overwrite a newer toggle
in Jellyfin.

The duration comes from the audience's play of the title where one
exists, and from the catalog's running time where none does. A watched
mark on a title with neither sends nothing, because a row with no
duration never reads as finished.

A movie's page ends its row with "Mark watched" on a film the audience
has not finished and "Clear progress" on one they started or finished.
The page now reads the standing play whole, so a film they finished
offers the clear and draws the "Watched" word. On a series page, enter
on a still opens that episode's own row in the header, with Play or
Resume and Start over and the two marks, and back closes it. So a play
from the episode wall takes two presses, as a film does from its
wall. A continue-watching card gets no key of its own: the four keys
every remote has already move and select, so the card's actions are on
the page the card opens.

## What the design must answer

- Where the one finished rule lives, so the browser, the outbound
  writer, and the thread rule read the same line, credits included.
- What a mark records as its time. A backfilled item with no
  `LastPlayedDate` is stored at `recorded = 1` and loses to every other
  row; a mark must not lose to an older play.

  Answered: the time of the press, as the browser's wall clock reads
  it in Unix seconds, which the progress role writes as `recorded`. A
  mark is newer than every play recorded before the press, and a play
  after it replaces it.
- How the reconcile at start avoids an echo: the outbound writer drops
  an inbound position within 1 s of one it wrote in the last 5
  minutes, and the reconcile must not write back what the writer sent.
- Whether the two actions need a confirmation on the screen. Clear
  removes a position that the person cannot get back.

  Answered: no confirmation. Each action is one press, and a later
  play of the title replaces the mark.

## Not in this plan

- `PlayCount`. The store keeps no count, and nothing here needs one.
- A mark for one person out of several at the screen.
- A CLI verb. The browser is where a person sees the row.

## How it will be proved

- Browser tests: mark watched on a film card removes the card; on an
  episode card, the next episode takes it; clear on either removes the
  container; a row at 0 makes no card.
- Progress role tests: a mark on the bus writes one row with the
  audience and the aliases, and the newer time wins.
- Jellyfin role tests, against the fake Jellyfin server the role's
  tests use: a mark sends `Played` and the position once; a replay
  below the line sends no `Played: false`; an unplayed toggle removes
  the card; the reconcile at start records a toggle made while the role
  was down.
- A drill on a cluster with Jellyfin: mark a film watched on the screen
  and see it played in Jellyfin; mark it unplayed in Jellyfin and see it
  stay off the row; restart the jellyfin role with a toggle made while
  it was down, and see the toggle arrive.
