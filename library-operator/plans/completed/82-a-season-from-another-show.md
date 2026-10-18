# 82, A season from another show

This plan, written on 2026-10-18, lets a person state that one season
of a series is, on one provider, a season of a different show. The
facts that ask a provider about a season or an episode then ask that
show. Built on 2026-10-18. The drill has not run.

## The problem

Providers do not agree on what a series is. Netflix's anthology
`Monster` is one series of four seasons on TheTVDB and on IMDb, and
Sonarr names its files that way:
`Monster (2022)/Season 02/S02E01 - ….mkv`. TMDb and TVmaze give each
installment its own show, with one season each:

| Installment | TheTVDB | TMDb | TVmaze |
|---|---|---|---|
| Dahmer (2022) | `389492` season 1 | `113988` season 1 | `50907` season 1 |
| Menendez (2024) | `389492` season 2 | `225634` season 1 | `68626` season 1 |
| Ed Gein (2025) | `389492` season 3 | `286801` season 1 | `86754` season 1 |
| Lizzie Borden (2026) | `389492` season 4 | `299939` season 1 | `87597` season 1 |

A series folder holds one id for each provider. No TMDb id names all
four seasons, so the facts that ask TMDb about a season or an episode
ask the wrong show, or a show that has no such season. A
`tvshow.nfo` that names TMDb `299939` makes the `episode-thumb` fact
write the Lizzie Borden stills beside the first eight Dahmer episodes,
and record an error for the other 27 episodes, because TMDb answers
404.

These facts ask a provider about one season or one episode with the
series' id and the folder's season number:

* `season-poster` from TMDb: `/3/tv/{id}/season/{n}/images`.
* `episode-thumb` from TMDb: `/3/tv/{id}/season/{n}/episode/{e}/images`.
* `season-poster` and `season-banner` from Fanart.tv, by the TheTVDB
  id and the season number.
* `marks` from TheIntroDB and IntroDB, by the TheTVDB, TMDb, or IMDb id
  and the two numbers.
* `rating.imdb` for an episode, by the IMDb id and the two numbers in
  the IMDb datasets.

Every other fact asks about the series as a whole.

## The design

### The mapping in `season.nfo`

Kodi and Jellyfin read a `season.nfo` in each season folder. Jellyfin
reads its `<seasonnumber>`, its `<seasonname>`, and its `<uniqueid>`
elements, and skips an element it does not know. A season's
`<uniqueid>` names the provider's own record of that season, so this
plan does not reuse it. A person adds one `<showid>` element for each
provider whose show differs from the series:

```xml
<season>
  <seasonnumber>2</seasonnumber>
  <showid type="tmdb" season="1">225634</showid>
  <showid type="tvmaze" season="1">68626</showid>
</season>
```

`type` names the provider, by the same names a `<uniqueid>` uses.
The text is the show's id on that provider. `season` is that show's
season number for this folder's season, and it defaults to the folder's
own season number. An episode keeps its episode number: `S02E03` of the
folder is episode 3 of season 1 of TMDb's `225634`. A `<showid>` with no
text, or with an unknown `type`, is skipped, and the walk logs one line
for it.

The walk reads `season.nfo` in each season folder. A changed
`season.nfo` is a changed file, so the next walk reads it again, and a
webhook rescan of the folder reads it at once.

### No catalog table

The mapping stays on the volume, and the catalog keeps no copy of it.
Every reader of the mapping already runs beside the volume: the walk,
the art phase, and the marks phase. The art phase reads a title's ids
from `tvshow.nfo` the same way. A table would add a write, a sweep, and
a second place where the mapping could be stale.

### The facts

These facts read the mapping of the season before they ask:

* `episode-thumb` and `season-poster` from TMDb.
* `season-poster` and `season-banner` from Fanart.tv, through a mapped
  `tvdb`.
* `marks` from TheIntroDB and IntroDB. TheIntroDB sends the season
  that goes with the id it names, so a mapped TMDb id goes with the
  mapped season, and an unmapped TheTVDB id goes with the folder's
  season.

For a provider that the season maps, the fact asks with the show's id
and the show's season. For any other provider, it asks as before.

`rating.imdb` for an episode does not read the mapping. It reads the
episode's own IMDb id first, which the episode's `.nfo` file holds, and
IMDb keeps the anthologies this plan covers as one series. A mapped
`imdb` can be added there when a library needs one.

### Reopening a season

Each attempt of these facts records the mapping it asked under, as one
line such as `tmdb:225634:1`, in the ledger's new `show` field. A
season the series asks as itself records no line. The walk reads each
series' `season.nfo` files before its ledgers, and it lifts an attempt
only while the season's mapping is the line the attempt recorded. An
attempt it does not lift leaves the catalog through the sweep, and the
fact's gap opens. The marks spans of such an attempt leave the rows
with it, and the marks phase drops them from the ledger when it asks
again, from the sources that did not answer as well.

An art file that the art phase wrote under another mapping also
records the line, on its ledger item. The walk takes that file out of
the rows, so no episode links to a still of another show, and the
season art gap opens. The art phase then writes over the file through
a door of its own. The door writes only while the file is not newer
than the second the ledger says the phase wrote it, so a file a person
or another tool replaced stays. A file another tool wrote, which the
ledger records as `existing`, is never written over.

### The pages

The libraries guide and the enrichment guide describe `season.nfo` and
`<showid>`, with the `Monster` table as the example.

## Considered and set aside

**Split the series into one folder per installment.** Each folder's ids
would then match TMDb and TVmaze exactly. But Sonarr manages the series
by its TheTVDB id, as one folder, and moves new files back into
`Season NN`. The split fights the tool that fills the library.

**A field on the `Library`.** A map from series path to season mapping
would keep the mapping in the cluster. A mapping describes the media,
not the cluster, so it belongs beside the media, where a second cluster
or Jellyfin reads it too. A person already fixes a wrong identity by
editing `tvshow.nfo`.

**`<uniqueid>` in `season.nfo`.** Jellyfin reads a season's
`<uniqueid>` as the provider's own season record, so a show id there
would give Jellyfin a wrong season id.

**An episode offset.** Some shows number episodes across seasons
differently on each provider. This plan maps whole seasons only, which
covers anthologies. An episode offset can be added to `<showid>` when a
library needs one.

## Proof

Tests seed a series whose `Season 02/season.nfo` maps TMDb and run:

* A mapping added, kept, or removed after the facts asked opens or
  keeps the `episode-thumb` and `marks` gaps, through a rescan into a
  catalog.
* A still or a season poster that TMDb wrote under another mapping
  leaves the rows.
* `episode-thumb` and `season-poster` ask TMDb for the mapped show and
  season, record the mapping, and replace a still of another show that
  nobody changed since.
* Fanart.tv's season poster reads the mapped show's season.
* TheIntroDB and IntroDB ask with a mapped id and its season.
* A skipped `<showid>` and an unreadable `season.nfo` are reported.

A drill adds the four `season.nfo` files to a `Monster` series folder,
turns on `episode-thumb` for the drill, and checks that each season's
episode thumbnails are its own installment's stills.
