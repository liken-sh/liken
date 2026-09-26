---
name: enrichment
description: "Enrich a library with titles, plots, ratings, art, and people from metadata providers, written as .nfo files and art files beside the media. Use when declaring a MetadataProvider, naming sources on a Library, or handing metadata to Jellyfin."
---

This skill is the guide at https://library.liken.sh/docs/guides/enrichment/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Enrich a library

Enrichment adds the information that the volume does not hold: a media
folder's title, plot, ratings, art, and people. The operator asks
metadata providers and writes their answers beside the media. Kodi and
Jellyfin read the resulting `.nfo` files and art files. The volume
remains the source of truth, and the catalog is derived from it.

## 1. Declare a provider

A `MetadataProvider` configures one provider. For providers that require
an API key, store the key in a `Secret` in the same namespace:

    apiVersion: v1
    kind: Secret
    metadata:
      name: tmdb-api-key
      namespace: media
    type: Opaque
    stringData:
      token: <your key>
    ---
    apiVersion: library.liken.sh/v1alpha1
    kind: MetadataProvider
    metadata:
      name: tmdb
      namespace: media
    spec:
      tmdb:
        secretRef:
          name: tmdb-api-key

The providers, and the facts each one serves:

* `tmdb`, The Movie Database: identity, overview, certification, its
  own rating, credits, poster, backdrop, logo, season posters, episode
  stills, trailers, and the people's ids, biographies, and headshots.
  Every library starts here, because identification happens through
  TMDb.
* `omdb`, OMDb: overview, certification, and the IMDb, Rotten
  Tomatoes, and Metacritic ratings. It answers on an IMDb id, which
  TMDb supplies. The free tier allows a thousand calls a day.
* `fanart`, Fanart.tv: art only, and the one source of clear art,
  banners, landscapes, disc art, and season banners.
* `tvmaze`, TVmaze: series only, and it needs no account. Declare it
  with an empty block, `tvmaze: {}`.
* `peertube`, one PeerTube instance: trailers only, and it needs no
  account. Declare it with the address of the instance,
  `peertube: {endpoint: https://tube.example}`. The trailer fact
  searches the instance by title, so pick an instance that publishes
  trailers.
* `archive`, the Internet Archive: trailers only, from its
  `movie_trailers` collection, and it needs no account. Declare it
  with an empty block, `archive: {}`. It holds trailers for many older
  films, and the operator asks it no faster than four times a second.
* `theintrodb`, TheIntroDB: marks only, the intro, recap, credits, and
  preview spans of movies and episodes. It finds a work by its TMDb id,
  and a key is optional. Declare it with an empty block,
  `theintrodb: {}`, or name a `Secret` under `secretRef` to use a key.
  Without a key it answers 500 asks a day for your address. With one it
  answers 1000 a day for your account, and it adds your own pending
  submissions to each answer.
* `introdb`, IntroDB: marks only, the intro, recap, credits, and
  post-credits spans of movies and episodes. It finds a work by its IMDb
  id and needs no account. Declare it with an empty block,
  `introdb: {}`.
* `imdb`, IMDb's published datasets: the IMDb rating of movies, series,
  and episodes, and the principal credits of movies and series, from
  files that IMDb replaces every day. It needs no account and has no
  daily limit. Declare it with an empty block, `imdb: {}`.
  [IMDb ratings and credits from the datasets](#imdb-ratings-and-credits-from-the-datasets)
  describes how the files are read and kept.

The operator checks that each provider answers with one call: when it
starts, when you edit the provider or its `Secret`, and then once an
hour. A provider that gives no usable answer is checked every five
minutes until it does. A refused key waits the hour, so edit its
`Secret` to have it checked at once. A `secretKeyRef` passes the key to
a phase container of the `Library`'s `Job`, so no long-running pod stores it.

    $ kubectl -n media get metadataproviders
    NAME    PROVIDER   READY   REASON      UPDATED   AGE
    tmdb    tmdb       True    Reachable             3d
    omdb    omdb       False   Refused               3d
    imdb    imdb       True    Reachable   9h        3d

`UPDATED` is for `imdb` alone: the oldest time IMDb replaced one of the
files the provider reads.

`spec.facts` narrows what one account serves.
[MetadataProvider](https://library.liken.sh/docs/reference/metadataproviders/) describes every
field.

## 2. Name the sources on the Library

A `Library` asks the providers it names in `spec.sources`, in that
order:

    spec:
      sources:
        - tmdb
        - omdb
        - fanart

A fact with one value, such as a plot or a certification, takes the
first provider that answers. A fact with a set of values, such as the
genres, takes the union of every provider that answers. Art takes the
first provider that holds an image. The `Sources` condition on the
`Library` reports whether every name resolves and every fact the
library needs has a provider.

Put `imdb` before `omdb` for the IMDb rating. The datasets rate every
title in one read of one file, and OMDb's thousand calls a day then go
to the plot, the certification, and the Rotten Tomatoes and Metacritic
ratings. Only `imdb` rates episodes, and only when it is the first
source that serves `rating.imdb`:

    spec:
      sources:
        - tmdb
        - imdb
        - omdb

Keep `tmdb` before `imdb` for the credits. IMDb's datasets hold about
nine people for each title, the billed cast and the key crew, and TMDb
holds the full cast. So `imdb` answers the credits of a title only where
no source before it answered: a title TMDb does not hold, or a
`Library` with no TMDb key.

## 3. What the phases do

Enrichment runs as phases of the `Library`'s `Job`, one container for
each phase. The operator runs one `Job` of a `Library` at a time. A walk
`Job` runs the walk and every phase the `Library`'s sources serve. A
`Job` that fills gaps runs no walk and only the phases whose gaps the
last report counted. The [scanning guide](https://library.liken.sh/docs/guides/scanning/#when-a-scan-runs)
says when each one starts.

The phases are `probe`, which reads each video's streams, `arrival`,
which records when a file was first seen, `identity`, which names each
title, `nfo`, which fills the `.nfo` file, `art`, which downloads the
images, `trailer`, which records where each title's trailers are,
`marks`, which records where each video's intro and credits are,
`contributors`, which fills the people, and, where the `Library` turns
them on, `trickplay` and `trailer-files`. A phase runs only where a
Ready source of the `Library` serves one of its facts. `probe` and
`arrival` ask no provider, so they always run.

All the phases start together, and each one reads its gap again
whenever the rows it reads change. So a phase works on a title as soon
as the phases before it have written that title's rows: `nfo`, `art`,
and `trailer` wait for the id `identity` writes, `marks` for the id and
the length `probe` measures, `contributors` for the people the credits
fact writes, `trickplay` for the length, and `trailer-files` for the
addresses `trailer` records. The art of the first title lands while
`identity` still works on the rest.

A phase ends when every phase it waits for has ended and one pass after
that found no work. Then it writes a mark on the `Job`'s `phases`
volume, and the `close` container waits for every mark before it writes
the `Job`'s run. Three phases edit the `.nfo` file of a title: `probe`
writes `<fileinfo>`, `identity` writes `<uniqueid>`, and `nfo` writes
its element groups. Each edit takes a lock on the `phases` volume for
that file, so no two of them overwrite each other.

A phase that fails writes a failed mark, and the phases that wait for
it finish what they have and end. The `Job` still succeeds, the
`enrich` run in `status.runs` names the failure, and the failed phase's
titles stay in its gap for the next `Job`.

    kubectl -n media get jobs -l library.liken.sh/library=movies
    kubectl -n media logs job/<job> -c nfo

### Trickplay

The scrub-bar thumbnails are the `trickplay` phase, which runs where
`spec.trickplay.enabled` is set. One title's decode runs for minutes,
so the phase starts no new title fifteen minutes into a run. It
finishes the title it has, and the rest stays in its gap. While that gap
is open, the operator starts a `Job` that fills gaps each time the
`Library` has no other `Job` running, so a backlog clears fifteen
minutes at a time and a webhook's folder never waits behind all of it.

The phase decodes on the node's GPU when `spec.trickplay.render` names
a DeviceClass. The operator keeps a `ResourceClaimTemplate` for the
`Library`, and a `Job` that runs the phase claims one device from it.
`ffmpeg` decodes through VA-API, and it falls back to software for a
codec the GPU refuses. With no render block the phase decodes in
software. With a render block on a cluster where no node offers such a
device, the pod stays `Pending`, and its events say so.

The tile directory is the one Jellyfin reads and writes. So the phase
accepts a directory Jellyfin made first and leaves it alone.

### Identification

The identity fact asks TMDb for the folder's title and runs a fixed
sequence of tests: the title, then the year, then a year on either side,
then, for a series, the episode names, then the runtime within five
minutes. The episode test reads the episode titles from the file names
of the folder's first season. It keeps a candidate whose season on TMDb
has at least two of them and at least half. So a series folder named
with the title alone identifies without an `.nfo` file. One survivor is the
answer, and its reason is recorded. Several survivors become candidates
in `.liken/identity.yaml`, and the title counts in `status.waiting`
until a person names the right `uniqueid` in the `.nfo`. A title no
provider can name counts in `status.unresolved`.

After identifying a title through TMDb, the identity phase requests its IMDb
and TVDB ids and writes any returned ids into the `.nfo` file. OMDb uses
the IMDb id to look up the title. Fanart.tv uses the TMDb id for a
movie and the TVDB id for a series. These ids identify the title;
OMDb and Fanart.tv still require their own API keys, configured through
each provider's `secretRef`.

### The write rule

Each fact owns a fixed group of elements in the `.nfo` file and writes
nothing outside it. The `overview` fact owns the plot, the tagline,
the genres, the studios, the premiere date, and the runtime. Each
rating owns its one element. `credits` owns the actors, directors,
and writers. Before a fact writes again, it hashes the group as it is
now and compares that hash with the one it recorded after its last
write. If the hashes differ, another writer changed that group. The
fact records a fight and does not write the group. `status.fights`
counts the fights.

An art file that already exists is never replaced. The fact records it
as answered and downloads nothing.

### People

The `credits` fact writes each title's cast and crew into
`.liken/credits.yaml`, and it gives each credited person one entry in
`.contributors/` at the library root. A credit looks for its entry by
id first. When the catalog holds an entry with one of the credit's
ids, the credit names that entry, whatever the spelling of the name.
A credit that holds an IMDb id and no TMDb id asks TMDb for the TMDb
id first, when the `Library`'s sources name a `Ready` `tmdb` provider.
When no id finds an entry, the credit uses the entry at the slug of
the name. A second person of the same name gets the slug with an id,
such as `tom-hanks-tmdb-992`.

A credit from IMDb's datasets names a person by the IMDb id alone. So
it finds the entry a TMDb credit wrote for the same person by that id,
or through the TMDb id that TMDb's find call gives for it, and the
person keeps one entry. An entry the datasets created holds the IMDb id,
and `contributor.ids` fills its TMDb id, its biography, and its
headshot the same way.

The `contributors` container fills each entry. `contributor.ids`
writes the birth date, the death date, and the person's ids in other
databases into `contributor.yaml`. For an entry that holds only an
IMDb id, it asks TMDb for the TMDb id first. `contributor.biography`
and `contributor.headshot` write `biography.txt` and `headshot.jpg`
beside the entry where no file of that name exists.

Two entries can hold one id, for example when two providers spell one
name two ways. After it writes the ids, `contributor.ids` merges each
group of entries that share an id into one entry:

- The entry at the slug of its own name, with no id suffix, stays. If
  no entry of the group is at such a slug, the entry with the most ids
  stays.
- The entry that stays gets every id of the group. `biography.txt` and
  `headshot.jpg` move to it where it has no file of that name.
- Each other entry keeps a `contributor.yaml` with one field,
  `mergedInto`, the path of the entry that stays. The catalog shows no
  person for such an entry.
- The next `Job` of the `Library` moves each credit that names a
  removed entry to the entry that stays. The nfo phase moves the
  credits, and it can end before the merge, so the move waits for that
  `Job`. The contributors phase of the same `Job` then deletes each
  removed entry that no credit names.

The merge leaves a group whole in two cases, and
`.liken/contributor.merge.yaml` in each entry of the group records
the reason. A `held` attempt means a person edited a
`contributor.yaml` of the group, and `status.fights` counts each entry
of it. A `conflict` attempt means two entries hold two different ids
in one scheme, so they are two people and one of them holds a wrong
id. The merge asks about the group again after thirty days. To hand
an edited entry back to the merge sooner, delete
`.liken/contributor.ids.yaml` and `.liken/contributor.merge.yaml` in
that entry. The next walk opens the gap, and `contributor.ids` then
reads the file as its own.

### Trailers

The `trailer` fact records links, not files. It asks every source
that serves it and writes what it finds to `.liken/trailer.yaml`
beside the title, one entry per video: the provider, the provider's
own key, the site the video plays from, the page to watch it on, its
name, its kind (`trailer`, `teaser`, or `spot`), its language, its
resolution where the provider states one, and a score from 1 to 100
with a one-line reason. The catalog's `trailers` table holds the same
rows.

The score ranks the videos of one title, so a screen can take the first.
A video keyed by the title's own id, as TMDb's are, starts at 100. A
video found by a search starts at 90 when its name has the same title
and year. It starts at 60 when the name has the title and no year at
all. A search result whose name has another title or another year is not
recorded. Then the kind takes points off, a teaser less than a TV spot.
A language the household does not prefer takes points off, and so does a
TMDb video that is not marked official. The reason says which of these
applied. Clips, featurettes, and other extras are not recorded at all.
Within one provider, videos with the same name collapse to the best one,
and at most five videos per provider are kept for a title. An Internet
Archive item is kept only when its own video runs eight minutes or less,
because the `movie_trailers` collection holds whole films beside the
trailers.

The preferred languages are the library's `spec.languages`, then the
household's `audioLanguages` from the media operator's
`MediaPreferences`, and `en` when neither names any.

A trailer file beside the title still plays as before.

### Intro and credits marks

The `marks` fact records where a video file's intro, recap, credits,
and preview are. It asks every source that serves it about each main
video of an identified movie, and of each episode of an identified
series, once the `probe` fact has measured the file's length. It writes
what it finds to `.liken/marks.yaml` in the folder that holds the file,
the folder whose `.liken/probe.yaml` records the same file:

    marks:
      - path: Game of Thrones - S01E02.mkv
        kind: intro
        end: 107000
        source: theintrodb
      - path: Game of Thrones - S01E02.mkv
        kind: intro
        start: 7007
        end: 106482
        source: theintrodb
      - path: Game of Thrones - S01E02.mkv
        kind: credits
        start: 3253000
        end: 3316000
        source: theintrodb

Each entry is one span: the file, the kind, the start and the end in
milliseconds from the start of the file, and the provider that answered.
An absent `start` is the start of the file, and an absent `end` is the
end of the file. A provider can answer several candidates for one kind,
from several submissions or several releases of the work, and the fact
records every one exactly as the provider answered it. It chooses none.
The catalog's `marks` table holds one row per span, and the media
browser sends every span to the `Play`, where the player in
`media-operator` reads them and offers the skip.

TheIntroDB reads the file's length from the ask, and it answers the
spans of the release whose length is closest, such as the theatrical
cut or the extended one. IntroDB reads no length. A file that holds two
episodes is asked about neither, because each provider places an
episode's spans in that episode's own file.

The community adds marks for a new episode or film in the days after
it comes out, so the fact asks again about a new work sooner than
about an old one. The release date the catalog holds for the movie or
the episode sets the wait after a find or a miss:

| Released | Asked again after |
|---|---|
| in the last 7 days | 1 day |
| 8 to 90 days ago | 7 days |
| more than 90 days ago, or no date | 30 days |

A file that holds two episodes takes the later of their dates. An
error waits one day, whatever the date.

The table applies only where every provider the `Library` names
answered. Where one provider failed, or was not asked, the attempt
records the result `partial`. The file keeps the spans the other
providers answered, keeps the spans the silent provider answered
before, and waits one day, so that provider is asked again the next
day.

A provider that answers `429` waits for the reset its headers name and
asks again. When a provider has spent its allowance for the day, the
reset is hours away, and the fact asks that provider nothing more in
this run. Every later file of the run is `partial`, and the run stops
when every provider has spent its allowance. A file that every provider
answered keeps the window in the table, so the next day's allowance
goes to the files the spent provider missed and not again to the ones
it answered.

Neither Jellyfin nor Kodi reads these marks. Jellyfin keeps its media
segments in its own database, and Kodi's `.edl` file is a different
format, so the fact writes no file other than its ledger.

### Trailer files

The `trailerfile` fact pulls one video file per title. It is off unless
`spec.trailers.enabled` is `true`, and it pulls nothing until the
`trailer` fact has recorded links.

The file lands at `<title>/trailers/<name>.mp4`. The name is the
trailer's own name with every character a file name cannot hold taken
out, and its length is capped.

The fact takes the highest-scored trailer whose site the operator can
fetch from. Today those sites are the Internet Archive and PeerTube,
never YouTube. From that trailer's files it takes the tallest file that
is no taller than the title's own feature, or the shortest above it
where none fits.

The fact never pulls for a title that already holds a trailer file
anywhere under its folder. A trailer a person placed by hand stays, and
the fact records nothing.

Every pull is remuxed to MP4 and checked with `ffprobe` before it
lands. The check requires a video stream and a length between 10
seconds and 8 minutes, so a TV spot passes and a whole film does not.
A file that fails the check never reaches a name the walk reads, and
the attempt records the error.

**A trailer is tens of megabytes per title, so a library of any size
adds gigabytes to the volume the first time this fact runs.**

The `trailer-files` phase of the `Library`'s `Job` runs the fact, and
two titles pull at once inside it. Like `trickplay`, the phase starts
no new title fifteen minutes into a run, and the next `Job` goes on
with the rest. The fact takes no `spec.refresh`.

### IMDb ratings and credits from the datasets

IMDb publishes its datasets as gzipped files at `datasets.imdbws.com`
for personal and non-commercial use. No `liken` image carries them, so
each cluster downloads them from IMDb. IMDb's terms require this credit
where the data is shown:

> Information courtesy of IMDb (https://www.imdb.com). Used with
> permission.

The `nfo` container reads its whole `rating.imdb` gap first. Then it
reads `title.ratings` once, from start to end, and keeps only the rows
of the titles in the gap, so one read serves one title or ten thousand.
The read starts when the container starts, and the other facts run
while it works. A run with no `rating.imdb` gap sends no request.

A movie or a series is found by the IMDb id in its `.nfo` file. An
episode is found by its own IMDb id where its `.nfo` file names one.
Otherwise the container reads `title.episode` once and finds the
episode by its season and episode numbers under the series' IMDb id. It
records the id it found in `.liken/rating.imdb.yaml`, so a later run
does not read `title.episode` again for that episode. A file that holds
two episodes gets no rating, because its ledger entry names one episode.

The credits read two files in sequence. The container reads
`title.principals` once and keeps the rows of the titles in its
`credits` gap, then reads `name.basics` once for the names of the
people those rows name. It does not read `name.basics` when every one
of those people already has an entry in `.contributors/`, because the
entry holds the name. Decompressing and reading the two files took
about 32 seconds on a cloud host in September 2026, however many titles
they fill, and the downloads add to that on the first run of a node. `actor`, `actress`, and `self`
rows are the cast, in IMDb's order, with the characters as the role.
`director` and `writer` rows are the crew. The other categories, such
as `producer` and `composer`, have no part in the credits and are not
written, and neither are `archive_footage` and `archive_sound`. The
credits are not asked for again on a timer. Set `credits` in
`spec.refresh` to ask again.

The rating is written into the `.nfo` file as OMDb writes it: the
`imdb` rating, out of 10, with the vote count. The container writes the
file only when the rating at one decimal changed. A change in the vote
count alone writes nothing, so a library's `.nfo` files do not all
change every month. The attempt records the `Last-Modified` time of the
`title.ratings` copy it read.

A rating from the datasets is asked for again after 30 days, but only
when IMDb has published a newer `title.ratings` than the one the
attempt read. A rating OMDb wrote counts as older, so a library that
moves from `omdb` to `imdb` moves each title within 30 days. A rating
that another tool, such as Radarr or Jellyfin, wrote into the `.nfo`
file has no attempt. The next `nfo` container that reads the datasets
reads that rating once and records an attempt, and it writes the file
only when the rating at one decimal differs. The gap count in the
status does not include these ratings, so they start no `Job` of their
own. While the
provider's `Stale` condition is `True`, IMDb has published no newer
file, and the operator starts no `Job` that fills gaps for these ratings
alone.

With a per-node `StorageClass` in the cluster, the operator keeps the
files on the claim `<provider>-datasets`, which the `nfo` container
mounts. Each node's copy fills the first time a run on that node needs
a file. Every later run asks IMDb with the copy's `ETag`, gets `304 Not
Modified` while the file is unchanged, and reads the copy from disk. A
new version replaces the copy only after the whole file arrived and its
gzip checksum is correct. Two runs on one node download a file once:
the second waits for the first and reads its copy. When the claim is
full or cannot be written, the container reads the file from IMDb and
logs the filesystem's error. With no per-node class, every run reads
the files from IMDb.

    kubectl -n media get metadataprovider imdb -o jsonpath='{.status.imdb.datasets}'
    kubectl -n media logs job/<job> -c nfo | grep -E 'title.ratings|title.principals'

### When a fact asks again

A miss lasts for thirty days and an error for one day, then the fact
asks again. The `marks` fact asks about a new work sooner, as the
section above describes. An attempt made before a title's release date
lasts only until that date. To ask one fact again for every title now, set its
time in `spec.refresh`:

    spec:
      refresh:
        overview: "2026-09-06T00:00:00Z"

Every attempt of that fact before the time no longer counts. A refresh
starts a `Job` that fills gaps, without a walk, as soon as the
`Library` has no other `Job` running, and a refresh set while a `Job`
runs starts another one after it. The fact
rewrites its own files and rows in place, and nothing is deleted.

`kubectl liken library reenrich movies` writes that field for you and
asks every fact again. Add `--only overview` to reopen one fact, and
`-n` to select the namespace. The command reads your kubeconfig, and
in bash it completes the library names.

The same map takes one key that is not a fact: `scan` asks for a full
walk of the library, and `kubectl liken library rescan movies` writes
it. The [scanning guide](https://library.liken.sh/docs/guides/scanning/) describes what it does.

## 4. The Jellyfin handover

Jellyfin reads the `.nfo` files and the art that this operator writes, under the
same names its own scraper uses. **Turn off Jellyfin's
`SaveLocalMetadata` for a library this operator enriches.** With it
on, two writers change the same `.nfo` files, and the fight check leaves
the file to Jellyfin.

## Reading progress

    kubectl -n media get library movies -o jsonpath='{.status.gaps}'
    kubectl -n media get library movies -o jsonpath='{.status.waiting} {.status.unresolved} {.status.fights}'
    kubectl -n media get library movies -o jsonpath='{.status.conditions[?(@.type=="Sources")]}'

`status.gaps` counts, per fact, the rows still to fill. Between walks,
the operator starts a `Job` that fills gaps with the phases whose counts
are above zero, once a refresh time or a source provider that turned
`Ready` has given them work since the last `Job` started. `trickplay` and
`trailer-files` run in such a `Job` while their counts are above zero,
because each run stops at its time limit.
