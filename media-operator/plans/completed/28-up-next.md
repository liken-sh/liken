# 28, Up next

Built, and drilled on `liken-1` on 2026-09-08 in release 2026.09.08-004.
A `Play` carries the work that follows it, the display offers that work
on the scrubber, and a select on the offer starts it. The
browser decides what follows and starts the next `Play`; this plan is
the media-operator half. The library-operator half is
[library-operator plan 53](https://github.com/liken-sh/library-operator/blob/main/plans/completed/53-up-next.md).

## The problem

When an episode ends, the `Play` finishes and the screen falls back to
the browser. A person who wants the next episode walks the browser to
the series page and presses play again. Nothing on the player knows
that a next episode exists, and nothing on the bus lets the player ask
for one.

The player cannot decide what comes next. Only the browser knows
whether the film was opened from a franchise page, a series page, or a
set, and only the browser reads the catalog. So the decision is the
browser's, and the player's part is to show it and to act on it.

## The design

### The Play carries what follows it

`spec.next` is an optional block on a `Play`:

```yaml
spec:
  next:
    reason: Next in Harbor Lights · S02
    title: E05 · The Long Tide
    detail: Harbor Lights · S02 · 45 min
    art: claim://shows/Harbor Lights/Season 02/E05-thumb.jpg
    request:
      library: living-room/shows
      selection: {episode: {series: "series:12", season: 2, episode: 5}}
```

`reason`, `title`, and `detail` are the three lines of the card, spelled
by whoever wrote the `Play`. The display draws them as given and reads
nothing into them. `art` is an image URI, resolved the way an item's
art is. `request` is an object the operator never reads
(`x-kubernetes-preserve-unknown-fields`). It is carried back verbatim
on the bus when the person asks for the next work, so the writer of the
`Play` gets its own words back.

A `Play` with no `next` offers nothing, and the display draws nothing
for it. The spec stays immutable, so the offer is fixed at creation.

`Next` is a typed struct in `api.go` beside `PlayItem`, and the pod
builder passes the whole block to both containers in one environment
variable, `MEDIA_NEXT`, as JSON with `art` rewritten to the resolved
in-pod path the way `MEDIA_PRESENTATIONS` rewrites an item's art.

### The display draws the offer

The command sidecar sends the block to the display once at start, as
`script-message-to display next <json>`, and again whenever the
display broadcasts `liken-presentation-request`, the way it replays the
presentation.

A new module, `upnext.lua`, draws the offer in two forms.

**The chip.** While the film plays and the OSD is summoned, the chip
is one line at the tiny type size, right-aligned above the right end
of the bar, at the muted grey: the word "Up next" and the `title`.

**The card.** When the playhead crosses ninety percent, the chip grows
into a card in the lower right, inside the bottom scrim. The card's
bottom edge stays above the row where the time label draws over the
playhead, so the card never covers the scrubber or its labels. The
card is the art at 440 by 248, then `reason` at the tiny size in
uppercase and the muted grey, `title` at the label size, and `detail`
at the small size in the muted grey. The card shows on its
own for a few seconds when it first rises, with the OSD hidden, and
then fades to a sliver: a tab in the fill green at the right edge, at
the card's height. Any press that summons the OSD brings the card
back with the OSD.

Ninety percent is the line the library's progress store counts a play
finished at, so the card rises at the moment the current work counts
as watched.

The art comes the way a logo does: the display asks the sidecar with
`liken-art-request` at a kind of `next` and the box size, and the
sidecar answers with a decoded bitmap fitted inside the box. A poster
draws letterboxed inside the same box.

**Focus.** The offer is a stop above the scrubber's fine stop. Up from
the scrubber lands on it, and down returns. The focused chip or card
takes the panel the choosers use, the dark fill inside the green
border. The focus reset on a summon still lands on the fine stop, so
the main button stays play-pause, and a select on the offer takes two
presses from a hidden OSD: one summons, up focuses, select acts. The
stop is present only while a `Play` carries a `next` block.

**Waiting.** A select on the chip grows it into the card, so a person
sees what the offer starts before a press starts it. A select on the
card broadcasts `liken-next`, the way a back at the bare video
broadcasts `liken-exit`. A card the rise raised takes one press. The
card dims and its last line reads "Starting". From then on the display routes every
press to nothing except back, which exits the run the way it always
does. The film plays on under the card until the operator ends the
`Play` or the film ends on its own.

### The sidecar publishes the ask

The sidecar reads `liken-next` off the IPC socket the way it reads
`liken-exit`, and publishes one message on the Player's commands topic:

```json
{"action": "play-next", "request": {"library": "...", "selection": {...}}}
```

`request` is the block's `request`, byte for byte. The Player's
commands topic is the one a delegate screen client already reads, so
the browser hears it with no new subscription. A sidecar whose `Play`
carries no `next` publishes nothing for the message.

The `media-screen` crate decodes the message into a new
`Moment::PlayNext(Vec<u8>)` carrying the raw `request` bytes, and the
browser acts on it. `on_command` answers `play-next` whether or not
the unit is idle, because the unit is never idle when this message
arrives.

### The newest Play wins

Two unfinished `Play`s naming one `Player` were undefined. The second
one's pod pended on the claim, and nothing ended the first. This plan
defines it: the newest `Play`, by creation time and then by name, is
the one that runs. On every pass the operator deletes every older
unfinished `Play` on that `Player`.

A deleting `Play` still holds the library's progress finalizer, so
garbage collection does not reach its pod until the store has recorded
the last position. The operator does not wait for that. A `Play` with a
deletion timestamp gets its pod deleted at once, and the claim frees,
so the pending pod starts.

The sidecar handles the pod's termination signal the way it handles a
back: it publishes the last report with the ended mark and quits mpv
cleanly. That is what records the position and lets the store release
the finalizer. It also fixes every other pod delete the operator
already does.

The rule is general. A play request from the browser while a film runs
now ends the film, from any source on the bus.

## What was set aside

**The playlist.** A `Play` already carries a list of items, and mpv
would roll into a second item on its own. But a second item has no
identity of its own: the aliases and the episode numbers are the
`Play`'s annotations, and the progress store keys on them. The list is
for an album, one work in several files. An episode is its own work and
its own `Play`.

**A presentation block on `next`.** The card could carry the same
typed presentation an item carries, and the display could spell the
lines. The browser already spells those lines on its own cards, and
Chris wants the card to mirror the browser. Three strings keep one
speller.

**An operator-to-pod stop verb.** A `stop` action on the `Play`'s
commands topic would let the operator end a run through the sidecar's
own exit path. A `Play` delete does the same through the owner
references, needs no new vocabulary, and the sidecar's clean exit on
the termination signal is the one piece of work either way.

**Playing the next work by itself.** When nobody takes the offer, the
`Play` finishes the way it does today. A person still watching presses
the button.

**A loading mark on the waiting card.** The brand's hexagon pulse lives
in the iced crate and has no Lua port today. The card dims and says
"Starting" instead.

## The proof

Drilled on `liken-1` on 2026-09-08. Over the bus, up, up, and select on
a Play with a `next` block published one `play-next`, the browser
answered with a play request in the same second, the next `Play` was
Pending at five seconds, and the old `Play` was gone and the new one
Running at eight seconds. The store held the old `Play` with its ended
mark at 607 of 712 seconds, and the new one running under its own
season and episode. Chris drilled the film chain from the remote, and
three findings from that drill are in the display: the first select
opens the card, the chip is measured by the face's own advances, and
the card's fill is darker. The first series drill also found rumqttc's
ten-kilobyte packet cap, which a whole-season play request crossed;
the reader now allows 256 KiB.

The plan as written:

Local first, on vega, in the headless harness: a `Play` with a `next`
block draws the chip, the card at ninety percent, the sliver, and the
waiting state, captured as frames. Then on `liken-1` with the browser
as the idle controller: an episode played from a series page offers
the next one, a select starts it, the first `Play`'s last position lands
in the store, and the continue-watching row moves to the next episode.
