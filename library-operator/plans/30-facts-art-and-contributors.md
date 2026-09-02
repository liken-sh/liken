# Facts, art, and contributors

Plan 30. The second enrichment build from [plan 27](27-enrichment.md).
Once a title has an id, this plan fills everything else: the `.nfo`
body, the art under Kodi's names, the credits with their people, and
the people themselves in `.contributors/`. Every concern here is one
more container in the enricher `Job` from plan 29. The `facts` and
`credits` concerns edit the `.nfo`, so they are init containers after
`identity`. The art and `contributors` concerns each write their own
files, so they are regular containers that run at once.

## The problem

Jellyfin writes the lab's `.nfo` files and art today, from TMDb, OMDb,
and Fanart. Replacing it means writing the same files at least as
well, and then writing what it never did: a person with ids and a
headshot on the volume, and a record of when the `.nfo` was last ours,
so two writers never fight over one file.

## The contract

- **The `facts` concern** writes the `.nfo` body: plot, tagline,
  ratings, certification, genres, studios, premiere, and the credits
  with their roles, in the form Kodi and Jellyfin read. It writes
  `nfo.yaml` beside it with the time, size, and mtime of the file it
  left. On its next run it compares. If the `.nfo` on disk no longer
  matches, another program wrote it, and the concern stops for that
  title and reports it in `Library` status.
- **The handover.** A `Library` opts into the `facts` concern. The
  plan's manual page says to turn off Jellyfin's `SaveLocalMetadata`
  for that library first, and the `nfo.yaml` check is what catches a
  library where that did not happen.
- **The art concerns**, one per type from plan 27's table, each
  writing one file under Kodi's local name beside the title, or in the
  season folder for season art. A `Library`'s `sources` order says
  which provider is asked first for each type.
- **The `credits` concern** writes `credits.yaml`, the link from each
  name in the `.nfo` to a directory in `.contributors/`, and creates
  that directory with `contributor.yaml` where none exists.
- **The `contributors` concern** fills a person's entry: the ids
  under every scheme the providers give, born, died, the biography,
  and `headshot.jpg`.
- **The `trickplay` concern** writes the thumbnail sprites and the
  WebVTT file beside the title, in the form the scrub bar reads today.
- **The catalog** gains a `contributors` table and a `credits` table,
  derived from these files by the scanner, keyed by library like every
  other table, with the ids in an `aliases`-shaped table so one person
  joins across libraries.
- **The attempts** per concern, with the retry interval per concern on
  the `Library`, defaulting to thirty days for a miss.

## Proof

On `liken-1`, one movies library with Jellyfin's saver off for it. The
drill records the count of titles per concern before and after, the
provider calls made against the key's limit, and the time to fill the
library. Then the rebuild drill: delete the catalog pod's claim, roll,
walk, and compare counts. Every fact returns from the volume alone.
Then the write-fight drill: edit one `.nfo` by hand, and the `facts`
concern stops for that title and says so in status.

## What is set aside

Writing the person's ids into the `<actor>` element. Neither Kodi nor
Jellyfin reads an id there, and a foreign element in the `.nfo` is at
risk on a rewrite. The link is in `credits.yaml`.

A shared `.contributors/` across libraries. Libraries may be separate
volumes, so each holds its own copy and the catalog joins them.

## What is not decided

Which image sizes to fetch, against the poster store's memory line
from plan 22. Whether a music library's artists get anything here
beyond `artist.nfo`, which the ecosystem already covers. How a person
replaces a poster they dislike, given that no enricher overwrites one.
