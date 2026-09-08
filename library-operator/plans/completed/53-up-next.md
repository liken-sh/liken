# 53, Up next

Built, and drilled on `liken-1` on 2026-09-08 in release 2026.09.08-007.
The browser decides what work follows the one it starts, writes that on
the `Play`, and starts it when the player asks. The
player's half is
[media-operator plan 28](https://github.com/liken-sh/media-operator/blob/main/plans/completed/28-up-next.md).

## The problem

When an episode ends, the screen falls back to the browser, and the
person walks to the series page and presses play again. The browser
knows the next episode, the next member of a franchise, and the next
film of a set. It tells nobody.

Only the browser can decide what comes next, because the answer
depends on where the person started. A film opened from a franchise
page continues along the franchise. An episode played from a series
page continues to the next episode. A film in a set continues to the
next film of the set. A film opened from a wall or a search result
continues to nothing, and that is fine.

## The design

### The play request carries what follows

The play request gains a `next` block, and the operator copies it onto
the `Play` as `spec.next`, with `art` stamped to a `claim://` reference
the way an item's art is:

```json
"next": {
  "reason": "Next in Harbor Lights · S02",
  "title": "E05 · The Long Tide",
  "detail": "Harbor Lights · S02 · 45 min",
  "art": "Harbor Lights/Season 02/E05-thumb.jpg",
  "request": {"library": "living-room/shows",
              "selection": {"episode": {"series": "series:12", "season": 2, "episode": 5}}}
}
```

`reason`, `title`, and `detail` are spelled the way the
continue-watching row spells a card: the reason line the row already
builds ("Next in The Saga", "Next in The Serial · S01"), the card's
caption, and the card's second line. `request` is the browser's own
`library` and `Selection`, serialized, and nothing else reads it.

### The browser knows what follows

The continue-watching row already answers "what comes next" for a
container: `containers::leaves` lists a series, a set, or a franchise
as ordered leaves, with a franchise's series members expanded into
episodes. The next work is the leaf after the current one in that
list. This plan reuses the leaves and adds nothing to the catalog.

`Step::Play` gains `next: Option<Next>`, and the page that emits the
step fills it:

* **The episode wall** names the next episode in aired order, from the
  stills it already holds. A wall reached through a franchise page
  names the next franchise leaf instead, which is the next episode
  while the run continues and the next member after it.
* **The movie page** names the next franchise leaf when the page was
  reached through a franchise page, else the next film of the film's
  set when it has one, else nothing.

To know it was reached through a franchise, a page carries an origin:
`Movie` and `Series` gain an optional `via: Option<InFranchise>`, set
by the franchise page when it opens them and by nothing else. The
`InFranchise` type already exists for the continue-watching card.

The art of the next work is the leaf's slot art, an episode still or a
film poster.

### One episode per Play

Before this plan, an episode's play request carried the chosen episode
and every later episode of its season, so mpv rolled through the
season on its own. That list is gone. A series play request carries
one item, and the show continues through `next`. Every episode is then
its own `Play` and its own row of progress, the request stays small,
and a person who walks away gets one episode. An album keeps its list,
because its tracks are one work.

### The browser starts the next work

The player publishes `{"action": "play-next", "request": {...}}` on
the Player's commands topic when the person takes the offer. The
`media-screen` crate turns it into `Moment::PlayNext(bytes)`, and the
browser decodes its own `request` and runs the same path a press on a
play button runs: resolve the items, read the resume position for that
work from the progress store, and publish the play request. That new
request carries its own `next`, computed the same way, so a run of
episodes chains.

The browser is covered by the film while this happens, and it stays
where it is. The player's `Play` ends, the browser shows for a moment
under the loading curtain, and the next `Play` covers it.

## What was set aside

**The player decides.** The player has no catalog and no memory of
where the person started. The browser has both.

**Reading the `Play` back at the press.** A screen pod holds no API
credential. The player carries the block and sends it back over the
bus, so the browser needs no read.

**A next for every film.** A film opened from a wall has no natural
successor. The row's reasons already say when one exists, and the
offer follows the same rule.

## The proof

Drilled on `liken-1` on 2026-09-08. A play request with a `next` block
for the second episode of a series reached the `Play`, and a select on
the offer over the bus brought the browser's play request for that
episode in the same second, with its own `next` for the third. The
old `Play` was gone and the new one running eight seconds after the
press, and the store held both: the old one ended at its last
position, and the new one under its own season and episode. The film
chain was drilled from the remote across a franchise. The first series
drill found that a whole-season play request crossed the bus client's
ten-kilobyte packet cap, which is why an episode of a long season
sometimes did not start before this plan; the crate's cap is raised,
and one episode per `Play` keeps the request small.

The plan as written:

Locally, in the browser's test fixture: a play from a series page
carries the next episode; a play from a movie page reached through a
franchise carries the next member; a play from a set member carries
the next film; a play from a wall carries nothing; and a `play-next`
moment publishes a play request for the named work with its own
`next`. Then on `liken-1`: the chain of two episodes end to end, with
the store's rows read after each.
