# A season mapping that another tool deletes

Open problem. The mapping from a season to another provider's show is
written in the season folder's `season.nfo`, as
[plan 82](../completed/82-a-season-from-another-show.md) states. Other
tools also manage files in that folder. A tool that deletes
`season.nfo` removes the mapping, and the operator then asks the
provider about the wrong show.

## What happens

A media manager can track the files beside an episode as extras of
that episode file. When it replaces the episode file, for example with
a better copy, it deletes the extras along with it. A manager can
count `season.nfo` among those extras, and nothing a person sets in it
needs to name `.nfo` files for that to happen. Replacing the files of
one season can then delete `season.nfo` in every season folder of the
series.

The next walk reads no mapping for those seasons, so every fact that
reads the mapping asks with the series' own ids from `tvshow.nfo`:

* For the season whose number exists on the series' show, the facts
  succeed with the wrong show. Episode stills and the season poster
  come from another installment, and the art phase writes them over
  the right ones.
* For a season the series' show does not have, the facts fail, and the
  files from the earlier mapping stay.

## What a user sees

The episode stills and the season poster of one season show another
season's installment. Nothing reports an error, because the facts
succeeded. A player that reads `season.nfo` loses the season's title
the next time it refreshes the season.

## Why the operator cannot keep the mapping itself

The mapping is something a person writes, and `season.nfo` holds the
only copy. Each attempt records the mapping it asked under, but the
walk reads those records as the state of a fact, not as the person's
intent. Once the file is gone, nothing tells the walk that the seasons
were mapped.

## Directions not yet weighed

* Read the mapping from a file in the folder's `.liken/` directory,
  which media managers skip because its name starts with a dot.
  `season.nfo` would then carry only what players read.
* Keep a copy of each mapping the walk has read, and report a season
  whose mapping disappeared, instead of asking with the series' ids.
* Leave the mapping where it is, and document the media managers that
  delete it.

## What is not measured

How many media managers delete `season.nfo`, and under which settings,
has not been measured. The one case seen so far deleted all of a
series' `season.nfo` files when one season's episode files were
replaced.
