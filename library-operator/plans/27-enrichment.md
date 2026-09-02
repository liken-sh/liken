# Enrichment

Plan 27. The design for the operator's third responsibility, and the
contracts that plans 28 to 31 build on. It replaces plan 11 and plan
24, which are in [`rejected/`](rejected/) with a note that says so.

## The problem

Jellyfin enriches the lab's volume today, and Radarr and Sonarr
organize it. That makes the catalog depend on programs outside the
cluster, and it caps what the catalog holds at what those programs
write. A person on the screen cannot follow a name to the other films
that person is in, because the sidecar names a person with no id and
no picture. A franchise that crosses films and series in story order
has no file anywhere, because no tool has a format for one. About a
fifth of the lab's movies have no sidecar at all, and the scanner can
only report that.

The design answers all of these with one rule: the volume holds every
fact, and the catalog is derived from the volume alone. The whole
catalog can be lost and rebuilt with no loss, because nothing was ever
written only to the catalog.

## The volume holds the truth

**Ecosystem files first.** A title's facts, its credits with their
roles, and its art are written in the formats Kodi and Jellyfin read:
the `.nfo` beside the title, and the art files under Kodi's names,
`poster`, `fanart`, `clearlogo`, `clearart`, `banner`, `landscape`,
`discart`, `keyart`, and `characterart`. Any player that reads the
folder reads what `liken` wrote.

**`liken` files for what the ecosystem cannot say.** A `.liken/`
directory in a title's folder, or in a season or album folder for the
items inside it, holds one file per writer:

| File | Writer | Holds |
|---|---|---|
| `identity.yaml` | the identity concern | the provider ids of each item, or the candidates that wait for a person |
| `credits.yaml` | the credits concern | the link from each name in the `.nfo` to its entry in `.contributors/` |
| `nfo.yaml` | the facts concern | the time `liken` wrote the `.nfo`, and the size and mtime it left |
| `<concern>.yaml` | that concern | its attempts: provider, time, and result, per item |

One file per writer is what lets many small enrichers run at once on a
network mount with no locks. No two of them ever open the same file for
write. The scanner reads the whole directory the way it reads the
`.nfo`, and lifts the attempts into the catalog as rows, so a gap query
can exclude a title that was tried.

**A miss is a fact with a date.** An attempt that found nothing is
recorded with its time. A concern asks again only after its retry
interval, because providers gain art and ids over time. So a missing
logo is "not found before this date", and never a hole an enricher
falls into every night.

**People have a store of their own.** `.contributors/` at a library's
root holds one directory per person, sharded by the first letter of a
slug in natural name order:

```
.contributors/
  k/keanu-reeves/
    contributor.yaml     name, ids under every scheme, born, died, biography
    headshot.jpg
```

The key is a slug because a person reads the file tree. The ids inside
carry every scheme the enrichers learn, `tmdb`, `imdb`, `tvdb`, and
`musicbrainz`, and the catalog joins one person across libraries by any
shared id. A movies library and a series library each hold their own
copy, because they may be separate volumes. Music artists keep their
ecosystem home, `artist.nfo` in the artist folder, so this store holds
the people the ecosystem gives no home: cast, directors, writers, and
later composers and producers.

**Franchises are a library kind.** A franchise crosses libraries, so no
member library can hold its file. A `Library` of kind `franchises`
holds one directory per franchise, with its file in story order and its
art. Members are named by provider id, and the catalog's `aliases`
table resolves each one to a row in whichever library of the namespace
holds it. Plan 31 has the file and the page.

## Writes

Every enricher writes onto the library's volume, and on the lab that
volume is the production copy. So the write rules are strict, and a
test enforces them.

- An enricher creates a file only where none exists. The exceptions
  are files inside `.liken/`, which `liken` owns, and the `.nfo` when
  `nfo.yaml` says the current one is ours.
- A write is a temporary file in the same directory, named with a
  `.liken-tmp-<job>` suffix, and a rename onto a name that does not
  exist. A crash leaves a stray temporary file and never a partial one.
- The only remove in the enrichers takes a name that matches the
  temporary suffix, checked before the call. The scanner treats that
  suffix as a junk name, so a stray never becomes a row.
- A wrong or stale file is reported in status and left in place. A
  person deletes it, and the gap loop refills it.
- Scanner `Job`s keep the read-only mount. Only enricher `Job`s mount
  the volume read-write.
- Every write goes through one package whose API has create and the
  temporary remove, and a test fails the build on `os.Remove`,
  `os.RemoveAll`, `os.Truncate`, or a rename onto an existing path
  anywhere else in the enricher code.

## Concerns

A concern is the unit of enrichment: one gap in the catalog, one pod
that fills it, one attempts file it writes. The vocabulary, in
dependency order:

| Group | Concerns | Reads | Writes |
|---|---|---|---|
| file | `probe`, `trickplay` | the media file itself | `<streamdetails>` in the `.nfo`, and the sprite files |
| identity | `identity` | name, year, and the probe's runtime | `identity.yaml` |
| facts | `facts`, `credits` | the providers | the `.nfo` body, `credits.yaml` |
| art | `poster`, `fanart`, `clearlogo`, `clearart`, `banner`, `landscape`, `discart`, `keyart`, `characterart`, `season-poster`, `episode-thumb`, `artist-thumb`, `artist-logo`, `album-cover`, `cdart` | the providers | one art file under Kodi's name |
| people | `contributors` | the providers | `.contributors/` |

Art is one concern per type so that a `Library` can take logos from one
provider and posters from another. The file group needs no provider and
no id, so it runs first, and the identity concern uses the runtime it
measured as a matching signal. Embedded tags in a music file are the
file group's facts as well, and for music they are the truth itself.

The scanner still opens no video file. A video probe over a whole
library is slow, so the `probe` concern opens the file once and writes
the answer into the `.nfo`, and a rebuild reads the `.nfo` and probes
nothing. A music scanner reads tags itself, because the ecosystem keeps
no per-track sidecar to write them into.

## Providers

A `MetadataProvider` is one account with one provider, with the same
discriminator-and-block shape as `Library`. It holds the `Secret` for
the key and states the concerns it serves. The `Library`'s ordered
`sources` list, in the schema since plan 02, orders the providers and
may narrow one to some concerns:

```yaml
kind: MetadataProvider
metadata: {name: tmdb}
spec:
  tmdb:
    secretRef: {name: tmdb-key}
  concerns: [identity, facts, credits, poster, fanart, clearlogo, contributors]
---
kind: Library
spec:
  sources:
    - provider: tmdb
    - provider: fanart
      concerns: [clearlogo, clearart, discart, banner]
    - provider: tvdb
```

For each concern, the first provider in the list that serves it is
asked first. The schema refuses a concern a provider does not list. The
order is on the `Library` and never on the provider, because two
libraries may disagree, and a children's library may want a different
art source.

What each provider serves, as checked on 2026-09-02: TMDb serves
posters, backdrops, and logos for a title, and a profile image, a
biography, and an `imdb_id` for a person. TVDB v4 serves a person with
an image, biographies, and `remoteIds` to other sites. TheAudioDB
serves artist thumb, fanart, and logo, and album cover and CD art, by
MusicBrainz id. Fanart.tv serves the clearlogo, clearart, discart,
banner, and landscape types that TMDb lacks, and the music art; that
one is from memory, because its documentation refused the fetch.

## Execution

One standing pod per namespace holds the durable catalog, and every
worker is a `Job` that holds no copy. Plan 28 builds it. In short: the
`Catalog` resource owns one pod with a Corrosion agent on the one
durable claim, with the agent's HTTP API on a `Service` in the
namespace. Scan `Job`s write rows through it. Enricher `Job`s read
their gaps through it and write only the volume. Screens keep their
own gossip copies and never touch it.

The operator holds a subscription on the catalog pod and creates
`Job`s from what it sees: a scan `Job` for a folder on a webhook, and
an enricher `Job` for a concern when a gap opens. It batches gaps by
folder, runs at most one `Job` per concern per library at a time, and
holds the rate limiter per provider key, because twenty `Job`s on one
key with no coordination end in a ban. Full walks are `CronJob`s.

## The plans

1. [Plan 28, the catalog pod](28-the-catalog-pod.md). The standing
   pod, the `Service`, scan `Job`s, the `CronJob`, and the webhook
   that creates a `Job`.
2. [Plan 29, identification](29-identification.md). `MetadataProvider`,
   the `probe` concern, `identity.yaml`, the gap loop, the write
   package and its test, and the counts in `Library` status.
3. [Plan 30, facts, art, and contributors](30-facts-art-and-contributors.md).
   The `.nfo` and its write record, the Jellyfin handover, the art
   concerns, `credits.yaml`, `.contributors/`, and trickplay.
4. [Plan 31, franchises](31-franchises.md). The library kind, the
   file, the table, the join, and the page.
5. [Plan 25, people on the screen](25-people-on-the-screen.md), which
   plan 30 unblocks.

Plan 12, the organizer, stays apart. Imports and moves are a different
loop with a different risk. Sets stay as plan 22 built them: derived
from the sidecars, release order, movies only.

## What is not decided

- Where the durable claim should be. A SQLite file on an NFS-backed
  claim can corrupt under a node loss, and the claim today binds the
  cluster's default `StorageClass`.
- How often the identity rule guesses wrong on a real library. Plan
  29's drill measures it before the rule ships as a default.
- The retry interval per concern. Thirty days for a miss is a guess.
- Which row the screen shows when two libraries hold one film. The
  franchise join and the movies wall face the same question.
- Whether a franchise may reach another namespace. The
  one-cluster-per-namespace rule says no today.
