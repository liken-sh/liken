---
name: enrichment
description: "Enrich a library with titles, plots, ratings, art, and people from metadata providers, written as .nfo sidecars and art files beside the media. Use when declaring a MetadataProvider, naming sources on a Library, or handing metadata to Jellyfin."
---

This skill is the guide at https://library.liken.sh/docs/guides/enrichment/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Enrich a library

Enrichment adds the information that the volume does not hold: a media
folder's title, plot, ratings, art, and people. The operator asks
metadata providers and writes their answers beside the media. Kodi and
Jellyfin read the resulting `.nfo` sidecars and art files. The volume
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

The operator checks that each provider answers with one call: when it
starts, when you edit the provider or its `Secret`, and then once an
hour. A provider that gives no usable answer is checked every five
minutes until it does. A refused key waits the hour, so edit its
`Secret` to have it checked at once. A `secretKeyRef` passes the key to
an enricher container, so no long-running pod stores it.

    $ kubectl -n media get metadataproviders
    NAME    PROVIDER   READY   REASON      AGE
    tmdb    tmdb       True    Reachable   3d
    omdb    omdb       False   Refused     3d

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

## 3. What the enricher does

The operator creates an enrich `Job` named `<library>-enrich` when a
walk has finished and a fact still has an open gap. The `Job` runs its
facts in order, each in a container of its own: `probe` reads each
video's streams, `arrival` records when a file was first seen,
`identity` names each title, `nfo` fills the sidecar, `art` downloads
the images, `trailer` records where each title's trailers are, `marks`
records where each video's intro and credits are, and `contributors`
fills the people.

    kubectl -n media get jobs -l library.liken.sh/library=movies,library.liken.sh/worker=enrich
    kubectl -n media logs job/movies-enrich -c nfo

### The trickplay Job

The scrub-bar thumbnails are a `Job` of their own, named
`<library>-trickplay`, because one title's decode runs for minutes and
the other facts must not wait behind it. It runs beside the enrich
`Job` when `spec.trickplay.enabled` is set and a video has no tile
directory. A webhook's folder gets one beside its enrich stage.

The `Job` decodes on the node's GPU when `spec.trickplay.render` names
a DeviceClass. The operator keeps a `ResourceClaimTemplate` for the
`Library`, and the `Job`'s pod claims one device from it. `ffmpeg`
decodes through VA-API, and it falls back to software for a codec the
GPU refuses. With no render block the `Job` decodes in software. With
a render block on a cluster where no node offers such a device, the
pod stays `Pending`, and its events say so.

    kubectl -n media get jobs -l library.liken.sh/library=movies,library.liken.sh/worker=trickplay

The tile directory is the one Jellyfin reads and writes. So the `Job`
accepts a directory Jellyfin made first and leaves it alone.

### Identification

The identity fact asks TMDb for the folder's title and runs a fixed
sequence of tests: the title, then the year, then a year on either side,
then, for a series, the episode names, then the runtime within five
minutes. The episode test reads the episode titles from the file names
of the folder's first season. It keeps a candidate whose season on TMDb
has at least two of them and at least half. So a series folder named
with the title alone identifies without a sidecar. One survivor is the
answer, and its reason is recorded. Several survivors become candidates
in `.liken/identity.yaml`, and the title counts in `status.waiting`
until a person names the right `uniqueid` in the `.nfo`. A title no
provider can name counts in `status.unresolved`.

After identifying a title through TMDb, the enricher requests its IMDb
and TVDB ids and writes any returned ids into the sidecar. OMDb uses
the IMDb id to look up the title. Fanart.tv uses the TMDb id for a
movie and the TVDB id for a series. These ids identify the title;
OMDb and Fanart.tv still require their own API keys, configured through
each provider's `secretRef`.

### The write rule

Each fact owns a fixed group of elements in the sidecar and writes
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

A provider that answers `429` waits for the reset its headers name and
asks again. When a provider has spent its allowance for the day, the
reset is hours away, and the fact asks that provider nothing more in
this run. The files it did not reach stay in the gap for a later run.

Neither Jellyfin nor Kodi reads these marks. Jellyfin keeps its media
segments in its own database, and Kodi's `.edl` sidecar is a different
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

The operator creates a `<library>-trailers-<walk>` `Job` beside the
enricher, on a catalog claim of its own, and two titles pull at once
inside it. The fact takes no `spec.refresh`.

### When a fact asks again

A miss lasts for thirty days and an error for one day, then the fact
asks again. An attempt made before a title's release date lasts only
until that date. To ask one fact again for every title now, set its
time in `spec.refresh`:

    spec:
      refresh:
        overview: "2026-09-06T00:00:00Z"

Every attempt of that fact before the time no longer counts. A refresh
starts an enricher on the next pass without a walk behind it, and a
refresh set while an enricher runs starts another one after it. The fact
rewrites its own files and rows in place, and nothing is deleted.

`kubectl liken library reenrich movies` writes that field for you and
asks every fact again. Add `--only overview` to reopen one fact, and
`-n` to select the namespace. The command reads your kubeconfig, and
in bash it completes the library names.

The same map takes one key that is not a fact: `scan` asks for a full
walk of the library, and `kubectl liken library rescan movies` writes
it. The [scanning guide](https://library.liken.sh/docs/guides/scanning/) describes what it does.

## 4. The Jellyfin handover

Jellyfin reads the sidecars and art this operator writes, under the
same names its own scraper uses. **Turn off Jellyfin's
`SaveLocalMetadata` for a library this operator enriches.** With it
on, two writers change the same sidecars, and the fight check leaves
the file to Jellyfin.

## Reading progress

    kubectl -n media get library movies -o jsonpath='{.status.gaps}'
    kubectl -n media get library movies -o jsonpath='{.status.waiting} {.status.unresolved} {.status.fights}'
    kubectl -n media get library movies -o jsonpath='{.status.conditions[?(@.type=="Sources")]}'

`status.gaps` counts, per fact, the rows still to fill. The operator
creates the next enrich `Job` while any count is above zero.
