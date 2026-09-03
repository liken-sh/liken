# Facts, art, and contributors

Plan 30. The second enrichment build from [plan 27](27-enrichment.md),
reshaped on 2026-09-03 after plan 29's drill. Once a title has an id,
this plan fills everything else: the `.nfo` body, the art under Kodi's
names, the credits with their people, and the people themselves in
`.contributors/`. It also grows `MetadataProvider` from one provider
to four, and renames the unit of enrichment from "concern" to "fact".

## The problem

Jellyfin writes the lab's `.nfo` files and art today, from TMDb, OMDb,
and Fanart.tv. Replacing it means writing the same files at least as
well, from the same sources, and then writing what it never did: a
person with ids and a headshot on the volume, and a record of which
provider answered for each fact, so a person can read why a title
looks the way it does.

Plan 29 built the machinery for one provider and one fact per
container. This plan needs about twenty facts and four providers, and
two rules from plan 29 do not survive that: "a concern is a container",
because seven containers in a row would edit one `.nfo`, and "the first
provider that serves a fact wins", because a rating from TMDb is not a
rating from IMDb, and a provider that serves certifications still has
none for half the titles.

## The words

A **fact** is the unit of enrichment: one gap in the catalog, one
ledger file in `.liken/`, one entry in a provider's list of what it
serves. The word replaces "concern" everywhere: the `attempts` table,
the gap map, the `MetadataProvider` schema, the enricher's role names,
and the manual. `facts` as the name of the plot group becomes
`overview`.

The vocabulary, in dependency order:

| Group | Facts | Reads | Writes |
|---|---|---|---|
| file | `probe`, `trickplay` | the media file | `<streamdetails>`, the sprite files and the WebVTT |
| identity | `identity` | name, year, runtime, and the providers | every `<uniqueid>` the provider knows, `identity.yaml` |
| nfo | `overview`, `certification`, `rating.tmdb`, `rating.imdb`, `rating.rottentomatoes`, `rating.metacritic`, `credits` | the providers | one element group each in the `.nfo`, and `credits.yaml` |
| art | `poster`, `backdrop`, `logo`, `clearart`, `banner`, `landscape`, `discart`, `season-poster`, `season-banner`, `episode-thumb` | the providers | one file under Kodi's name |
| people | `contributor.ids`, `contributor.biography`, `contributor.headshot` | the providers | `.contributors/<letter>/<slug>/` |

`overview` holds the plot, the tagline, the genres, the studios, the
premiere, and the runtime, because every provider that serves any of
them serves all of them in one call. `certification` stands alone
because OMDb's US ratings are better than TMDb's and a person may want
that one from a different source. A rating is one fact per site,
which is what lets two providers serve the same site's rating and the
first one answer.

## The contract

- **`MetadataProvider` grows one block per provider.** `spec.tmdb`,
  `spec.omdb`, `spec.fanart`, and `spec.tvmaze`, exactly one of them,
  enforced by CEL. Each block holds what that provider needs: a
  `secretRef` for TMDb, OMDb, and Fanart.tv, and nothing for TVmaze.
  `spec.facts` is optional and narrows what the provider serves; when
  it is absent the provider serves everything the operator's table says
  it can. `status.facts` lists what it serves right now, which is the
  table narrowed by the spec, and empty while the provider is not
  `Ready`. A `FACTS` printer column shows it. The `Ready` reasons stay:
  `Reachable`, `NoSecret`, `Refused`, `Unreachable`.
- **The table.** The operator holds which provider can serve which
  fact, and it is the manual's table too, generated from the same
  source. As designed on 2026-09-03:

  | Fact | TMDb | OMDb | Fanart.tv | TVmaze |
  |---|---|---|---|---|
  | `identity` | movies, series | | | series |
  | `overview`, `credits` | yes | yes | | series |
  | `certification` | yes | yes | | |
  | `rating.tmdb` | yes | | | |
  | `rating.imdb`, `rating.rottentomatoes`, `rating.metacritic` | | yes | | |
  | `poster`, `backdrop`, `logo`, `episode-thumb`, `season-poster` | yes | | yes | series |
  | `clearart`, `banner`, `landscape`, `discart`, `season-banner` | | | yes | |
  | `contributor.ids`, `contributor.biography`, `contributor.headshot` | yes | | | |

  TVmaze's columns are from memory and the build checks them. The
  IMDb datasets are not here: they are bulk files with a store, and
  [plan 33](33-the-imdb-datasets.md) has them.
- **Identity writes every id.** After the ladder finds a TMDb id, one
  call to TMDb's external ids gives the IMDb id, and for a series the
  TheTVDB id. Every one goes into the `.nfo` as its own `<uniqueid>`,
  and the scanner lifts each into `aliases`. This is what makes OMDb,
  which keys on IMDb ids, and Fanart.tv's series endpoint, which keys
  on TheTVDB ids, reachable without an account with either database.
- **Two rules for who answers.** A single value, such as a plot, a
  certification, or one site's rating, comes from the first provider in
  the `Library`'s `sources` that answers, not the first that serves.
  A set, such as the genres, the studios, or a person's ids, is the
  union of every provider that answers. The ledger records which
  provider answered each fact, so `.liken/certification.yaml` says
  `provider: omdb` for a title TMDb had no certification for.
- **A container is a phase.** The enricher `Job` keeps its shape from
  plan 29, with fewer containers than facts. Init containers, in order:
  the Corrosion sidecar, `probe`, `identity`, then one `nfo` container
  that runs every fact of the nfo group in order, with one read and one
  write of the `.nfo` per title. Regular containers, at once: `art`,
  which fetches every art type, `contributors`, and `trickplay`. Each
  container names the facts it runs in `LIBRARY_FACTS`, so the pod
  still reads as the sequence. Gaps, attempts, ledgers, and status stay
  per fact.
- **Each fact owns its elements.** A fact edits only the elements it
  writes, through plan 29's one-element edit, and every other byte of
  the `.nfo` stays. Its ledger records what it wrote and when. On the
  next run it compares: if its element on disk is not what it wrote,
  another writer has that element, and the fact stops for that title
  and reports it in `Library` status as a fight. `nfo.yaml` from plan
  27 goes; the ledgers carry the check. A ledger item gains `provider`
  and `wrote`, a hash of the element it left.
- **The handover.** A `Library` opts into the nfo group by listing a
  provider that serves it in `sources`. The manual page says to turn
  off Jellyfin's `SaveLocalMetadata` for that library first, and the
  fight check is what catches a library where that did not happen.
- **A rate limit stops the container.** OMDb's free tier is a thousand
  calls a day. A `429` still waits its cooldown and tries again as in
  plan 29, but a limit a provider states for the day ends that
  provider's work for the run: the remaining titles keep their gaps,
  the container logs the count it left, and the next run continues.
  No container sleeps for hours inside a `Job`.
- **Art is written where none exists.** An art fact writes its file
  under Kodi's local name beside the title, or in the season folder
  for season art, only when no file of that name exists. A poster
  Jellyfin wrote stays. A person who wants ours deletes the file, and
  the next run writes it. The art container fetches images and has a
  memory line of its own, above the scanner's.
- **The `credits` fact** writes the `<actor>` elements with role and
  order, and `credits.yaml`, the link from each name to a directory in
  `.contributors/`, and creates that directory with `contributor.yaml`
  where none exists. The person's ids never go into the `<actor>`
  element, because neither Kodi nor Jellyfin reads them there.
- **The contributor facts** fill a person's entry: `contributor.ids`
  writes the ids under every scheme the providers give, born, and
  died; `contributor.biography` writes the text; and
  `contributor.headshot` writes `headshot.jpg`. Each is a fact so a
  person with no headshot at TMDb is a miss with a date, not a person
  the enricher opens every night.
- **The `trickplay` fact** writes the thumbnail sprites and the WebVTT
  file beside the title, in the form the scrub bar reads today, from
  the file alone.
- **The catalog** gains a `contributors` table and a `credits` table,
  derived from these files by the scanner, keyed by library like every
  other table, with the ids in an `aliases`-shaped table so one person
  joins across libraries. The `attempts` table gains a `provider`
  column.
- **Attempts** per fact, as in plan 29, with the retry interval per
  fact on the `Library` and thirty days for a miss.

## The Job

```yaml
kind: Job
metadata: {name: movies-enrich-<run>, namespace: media}
spec:
  template:
    spec:
      initContainers:
        - name: corrosion            # native sidecar, restartPolicy: Always
        - name: probe
          env: [{name: LIBRARY_FACTS, value: probe}]
        - name: identity
          env: [{name: LIBRARY_FACTS, value: identity}]
        - name: nfo
          env: [{name: LIBRARY_FACTS, value: "overview,certification,rating.tmdb,rating.imdb,rating.rottentomatoes,rating.metacritic,credits"}]
      containers:
        - name: art
          env: [{name: LIBRARY_FACTS, value: "poster,backdrop,logo,clearart,banner,landscape,discart,season-poster,season-banner,episode-thumb"}]
        - name: contributors
          env: [{name: LIBRARY_FACTS, value: "contributor.ids,contributor.biography,contributor.headshot"}]
        - name: trickplay
          env: [{name: LIBRARY_FACTS, value: trickplay}]
        - name: enrich                # writes the runs row, waits for the echo
```

Every provider key the `Library`'s `sources` reach rides into every
container that asks a provider, as `<PROVIDER>_TOKEN` from the
provider's `secretRef`, the way `TMDB_TOKEN` does today.

## Proof

On `liken-1`, against the lab's movies and series libraries, with
Jellyfin's saver off for both. The drill records the count of titles
per fact before and after, the provider calls made against each key's
limit, and the time to fill each library. The rebuild drill: delete
the catalog pod's claim, roll, walk, and compare counts, so every fact
returns from the volume alone. The fight drill: edit one `<plot>` by
hand, and `overview` stops for that title and says so in status. The
who-answered drill: a title TMDb has no certification for gets one
from OMDb, and its ledger says so. The rate drill: a key past its limit
ends the run with gaps left and no sleep. A stripped title, metadata
only, comes back whole: ids, `.nfo` body, art, credits, and its people.

## What is set aside

Writing the person's ids into the `<actor>` element. Neither Kodi nor
Jellyfin reads an id there, and a foreign element in the `.nfo` is at
risk on a rewrite. The link is in `credits.yaml`.

A shared `.contributors/` across libraries. Libraries may be separate
volumes, so each holds its own copy and the catalog joins them.

Music providers. MusicBrainz and TheAudioDB are a column of the table
that waits for a music library.

## What is not decided

Which image sizes to fetch, against the poster store's memory line
from plan 22. Whether TVmaze earns its column once the build reads its
fields. How the browser shows a fight to a person, which is a later
screen plan.
