---
title: Enrich a library
weight: 60
---

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
  stills, and the people's ids, biographies, and headshots. Every
  library starts here, because identification happens through TMDb.
* `omdb`, OMDb: overview, certification, and the IMDb, Rotten
  Tomatoes, and Metacritic ratings. It answers on an IMDb id, which
  TMDb supplies. The free tier allows a thousand calls a day.
* `fanart`, Fanart.tv: art only, and the one source of clear art,
  banners, landscapes, disc art, and season banners.
* `tvmaze`, TVmaze: series only, and it needs no account. Declare it
  with an empty block, `tvmaze: {}`.

The operator reads the `Secret` once per pass, to check the provider
answers. The key reaches an enricher container through a
`secretKeyRef`, so no standing pod holds it.

    $ kubectl -n media get metadataproviders
    NAME    PROVIDER   READY   REASON      AGE
    tmdb    tmdb       True    Reachable   3d
    omdb    omdb       False   Refused     3d

`spec.facts` narrows what one account serves.
[MetadataProvider](/docs/reference/metadataproviders/) describes every
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
the images, `contributors` fills the people, and `trickplay` builds
the scrub-bar thumbnails when `spec.trickplay.enabled` is set.

    kubectl -n media get jobs -l library.liken.sh/library=movies,library.liken.sh/worker=enrich
    kubectl -n media logs job/movies-enrich -c nfo

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

### When a fact asks again

A miss stands for thirty days and an error for one day, then the fact
asks again. An attempt made before a title's release date stands only
until that date. To ask one fact again for every title now, set its
time in `spec.refresh`:

    spec:
      refresh:
        overview: "2026-09-06T00:00:00Z"

Every attempt of that fact before the time no longer counts. The fact
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
