---
name: enrichment
description: "Enrich a library with titles, plots, ratings, art, and people from metadata providers, written as .nfo sidecars and art files beside the media. Use when declaring a MetadataProvider, naming sources on a Library, or handing metadata to Jellyfin."
---

This skill is the guide at https://library.liken.sh/docs/guides/enrichment/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Enrich a library

Enrichment fills what the volume does not hold: which title a folder
is, its plot, its ratings, its art, and its people. The operator asks
metadata providers and writes the answers beside the media, as the
`.nfo` sidecars and art files Kodi and Jellyfin read. The volume stays
the source of truth, and the catalog is derived from it.

## 1. Declare a provider

A `MetadataProvider` is one account with one provider. The key is in
a `Secret` in the same namespace:

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

The operator reads the `Secret` once per pass, to check the provider
answers. The key reaches an enricher container through a
`secretKeyRef`, so no standing pod holds it.

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
the images, `trailer` records where each title's trailers are, and
`contributors` fills the people.

    kubectl -n media get jobs -l library.liken.sh/library=movies,library.liken.sh/worker=enrich
    kubectl -n media logs job/movies-enrich -c nfo

### The trickplay Job

The scrub-bar thumbnails are a `Job` of their own, named
`<library>-trickplay`, because one title's decode runs for minutes and
the other facts must not wait behind it. It runs beside the enrich
`Job` when `spec.trickplay.enabled` is set and a video has no tile
directory, and a webhook's folder gets one beside its enrich stage.

The `Job` decodes on the node's GPU when `spec.trickplay.render` names
a DeviceClass. The operator keeps a `ResourceClaimTemplate` for the
`Library`, the `Job`'s pod claims one device from it, and `ffmpeg`
decodes through VA-API and falls back to software for a codec the
GPU refuses. With no render block the `Job` decodes in software. With
a render block on a cluster where no node offers such a device, the
pod stays `Pending`, and its events say so.

    kubectl -n media get jobs -l library.liken.sh/library=movies,library.liken.sh/worker=trickplay

The tile directory is the one Jellyfin reads and writes, so a directory
Jellyfin made first is the answer, and the `Job` leaves it alone.

### Identification

The identity fact asks TMDb for the folder's title and runs a fixed
sequence of tests: the title, then the year, then a year on either side,
then, for a series, the episode names, then the runtime within five
minutes. The episode test reads the episode titles from the file names
of the folder's first season and keeps a candidate whose season on TMDb
carries at least two of them and at least half. So a series folder
named with the title alone identifies without a sidecar. One survivor
is the answer, and its reason is recorded. Several survivors become candidates in
`.liken/identity.yaml`, and the title counts in `status.waiting` until
a person names the right `uniqueid` in the `.nfo`. A title no provider
can name counts in `status.unresolved`.

Once a title has a TMDb id, one more call writes every other
database's id into the sidecar, so OMDb and Fanart.tv become reachable
with no account of their own.

### The write rule

Each fact owns a fixed group of elements in the sidecar and writes
nothing outside it. The `overview` fact owns the plot, the tagline,
the genres, the studios, the premiere date, and the runtime. Each
rating owns its one element. `credits` owns the actors, directors,
and writers. Before a fact writes, it checks that the group still
hashes to what it wrote last time. If another writer changed it, the
fact records a fight and writes nothing. `status.fights` counts
them.

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

The score ranks the videos of one title, so a screen can take the
first. A video keyed by the title's own id, as TMDb's are, starts at
100. A video found by a search starts at 90 when its name carries the
same title and year, and at 60 when the name carries the title and no
year at all; a search result whose name carries another title or
another year is not recorded. Then the kind takes points off, a teaser
less than a TV spot, and so does a language the household does not
prefer, and so does a TMDb video that is not marked official. The
reason says which of these applied. Clips, featurettes, and other
extras are not recorded at all. Within one provider, videos with the
same name collapse to the best one, and at most five videos per
provider are kept for a title. An Internet Archive item is kept only
when its own video runs eight minutes or less, because the
`movie_trailers` collection holds whole films beside the trailers.

The preferred languages are the library's `spec.languages`, then the
household's `audioLanguages` from the media operator's
`MediaPreferences`, and `en` when neither names any.

A trailer file beside the title still plays as before.

### Trailer files

The `trailerfile` fact pulls one video file per title. It is off unless
`spec.trailers.enabled` is `true`, and it pulls nothing until the
`trailer` fact has recorded links.

The file lands at `<title>/trailers/<name>.mp4`. The name is the
trailer's own name with every character a file name cannot carry taken
out, and its length is capped.

The fact takes the highest-scored trailer whose site the operator can
fetch from. Today those sites are the Internet Archive and PeerTube,
never YouTube. From that trailer's files it takes the tallest file that
is no taller than the title's own feature, or the shortest above it
where none fits.

A title that already holds a trailer file anywhere under its folder is
never pulled for. A trailer a person placed by hand stays, and the fact
records nothing.

Every pull is remuxed to MP4 and checked with `ffprobe` before it
lands. The check requires a video stream and a length between 10
seconds and 8 minutes, so a TV spot passes and a whole film does not. A file that fails the check never reaches a name
the walk reads, and the attempt records the error.

**A trailer is tens of megabytes per title, so a library of any size
adds gigabytes to the volume the first time this fact runs.**

The operator stands a `<library>-trailers-<walk>` `Job` beside the
enricher, on a catalog claim of its own, and two titles pull at once
inside it. The fact takes no `spec.refresh`.

### When a fact asks again

A miss stands for thirty days and an error for one day, then the fact
asks again. An attempt made before a title's release date stands only
until that date. To ask one fact again for every title now, set its
time in `spec.refresh`:

    spec:
      refresh:
        overview: "2026-09-06T00:00:00Z"

Every attempt of that fact before the time no longer counts. A refresh
starts an enricher on the next pass without a walk behind it, and a
refresh set while an enricher runs starts another one after it. The fact
rewrites its own files and rows in place, and nothing is deleted.

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
