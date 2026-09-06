---
title: Scanning
weight: 30
---

# Scanning

A scan walks a library's root and writes what it finds into the
namespace's catalog. This guide describes what the walk reads, when
it runs, how it removes what is gone, and what happens when a
`Library` is deleted.

## What a scan reads

The scanner reads the layout Kodi and Jellyfin read, so a volume those
players already organize needs no change.

### Movies

One folder per title. A folder is a title folder when it holds
`movie.nfo` or a video file. Any other folder is a grouping folder,
and the walk descends through it, up to eight levels deep:

    movies-pvc/
      Action/
        Example Movie (2019)/
          Example Movie (2019).mkv
          movie.nfo
          folder.jpg
          fanart.jpg
          Example Movie (2019).trickplay/
          Extras/
            Making Of.mkv
          Trailers/
            Example Movie (2019)-trailer.mkv

A readable `movie.nfo` with a title names the movie. Without one, the
folder name is parsed as `Title (Year)` or `Title [Year]`, or cut at
the first release token such as `bluray` or `x264`. A folder with no
sidecar and no year is counted in `status.unidentified` and cataloged
under its folder name.

### Series

One folder per series, directly under the root. Episodes are the files
in the series folder and in its season folders, one level down:

    series-pvc/
      Example Series/
        tvshow.nfo
        folder.jpg
        Season 02/
          season02-poster.jpg
          Example Series - S02E05.mkv
          Example Series - S02E05.nfo
          Example Series - S02E05-thumb.jpg
          Example Series - S02E05.en.srt
        Specials/
          Example Series - S00E01.mkv

`Season NN` is season N, and `Specials` is season 0. An episode's
number comes from a marker in its file name, `s02e05` or `2x05`, and a
range such as `s04e10-e11` names two episodes in one file. Both play
the file from the start, because nothing on the volume marks where the
second begins. The season comes from the folder first, then from the
marker.

### Extras and file kinds

A folder named `extras`, `featurettes`, `trailers`, `behind the
scenes`, `deleted scenes`, `interviews`, `scenes`, `shorts`, `clips`,
or `other` beside a feature or a season is read one level deep. Videos
under `trailers` are trailers, and the rest are extras.

Every file gets a row. The scanner classifies each one as `video`,
`audio`, `subtitle`, `image`, `metadata`, `trickplay`, or `other`
from its name, its folder, and one `stat`. It opens no file to
classify it. A subtitle's language is the tag in its name, and `hi`
after a language tag marks it hearing-impaired. Dot-named entries,
`Thumbs.db`, `desktop.ini`, and the trash and service directories of
common NAS systems are skipped.

### Sidecars and art

The scanner reads `movie.nfo`, `tvshow.nfo`, and the `.nfo` beside
each episode, leniently, because Jellyfin writes bare ampersands in
URLs. It reads the title, the year, the plot, the genres, the people,
the ratings, and the provider ids in `uniqueid` elements, with
`imdbid`, `tmdbid`, and `tvdbid` as fallbacks.

Art uses Kodi's names: `poster.jpg`, `fanart.jpg`, `clearlogo.png`,
`clearart.png`, `banner.jpg`, `landscape.jpg`, and `disc.png` in the
title folder, `season02-poster.jpg` beside `tvshow.nfo`, and
`<episode>-thumb.jpg` beside the episode. The scanner also accepts
`folder.jpg` and name-prefixed forms such as `<title>-poster.jpg`.

### The `.liken/` directory

Beside a title, a dot-named directory holds what the sidecar cannot
say: one YAML file per fact, named for the fact. `identity.yaml` holds
the provider ids, or the candidates left for a person to choose from.
`arrival.yaml` holds when each video file was first seen. Every other
`<fact>.yaml` holds what that fact wrote, which provider answered, and
its attempts. One file per writer is what lets several enrichers run
at once on a network mount with no locks. The scan reads these files
and never writes them.

### `.contributors/`

At the library root, one directory per credited person, sharded by
the first two characters of the person's slug. Each holds
`contributor.yaml` with the name and the provider ids, and, once the
enricher fills them, `biography.txt` and `headshot.jpg`. The walk
reads this directory after the titles. It is the one dot-named
directory the walk enters.

## When a scan runs

Every scan is a `Job`. The full walk runs from a `CronJob` named
`<library>-scan` on `spec.scan.schedule`, once an hour by default.
A walk that runs past its next turn skips that turn, because the
catalog claim admits one writer. A [webhook](/docs/guides/webhooks/)
runs a one-off `Job` that rescans one folder.

    kubectl -n media get cronjob movies-scan
    kubectl -n media get jobs -l library.liken.sh/library=movies,library.liken.sh/worker=scan
    kubectl -n media create job movies-scan-now --from=cronjob/movies-scan

A scan `Job` writes a `runs` row when it starts and another when it
finishes, then waits until the namespace's reporter echoes that run
back over the bus before it exits. So a `Job` that completed is a
`Job` whose counts reached the `Library`'s status.

## Mark and sweep

Each full walk has an epoch. The walk marks every id, path, and link
it reads with that epoch, and a prune pass deletes every row of this
library the epoch did not mark, in batches of five hundred.

Two guards keep a bad walk from emptying a library. A walk that could
not read every directory, or that found less than half of what the
catalog holds, is incomplete: it writes what it read and prunes
nothing, and the log says so:

    incomplete walk: could not read the whole volume, keeping the last counts

A prune whose epoch marked nothing at all is refused as an error.
`status.removedLastSweep` reports what the last sweep removed, so a
mass delete is visible without a shell.

The walk itself runs eight workers over a shared pool of directories,
which keeps a network volume busy without a burst large enough to slow
a player.

## Deleting a Library

A `Library` carries a finalizer, and deleting it starts a departure
that removes its rows from the namespace's catalog. The operator
deletes the `CronJob`, waits for any scan or enrich `Job` to finish,
then runs a cleanup `Job` named `<library>-cleanup` that deletes the
rows in batches through its own catalog agent. The finalizer clears
once the cleanup `Job` succeeded and the reporter echoed its run back.

While this runs, the phase is `Departing`, and the `Departing`
condition names the step: `ScanRunning`, `EnrichRunning`, `Sweeping`,
`AwaitingEcho`, or `Blocked` when the cleanup `Job` keeps failing or
the namespace holds two `Catalogs`. The operator never gives up on a
timer. It reports the blocker for as long as the object is deleting.

A namespace with no `Catalog` releases at once, because nothing there
holds the rows. A library whose own catalog claim is already gone gets
a fresh, empty one for the cleanup `Job`, whose agent receives the rows
over gossip and then sweeps them.

## Reading progress

    kubectl -n media get library movies -o jsonpath='{.status.phase} titles={.status.titles} unidentified={.status.unidentified} waiting={.status.waiting} gaps={.status.gaps}{"\n"}'
    kubectl -n media logs -l library.liken.sh/library=movies,library.liken.sh/worker=scan -c scanner --tail=100

A finished walk logs its counts:

    walk complete: 128 titles from 131 folders, 3 unidentified, 0 removed, in 41s

`status.unidentified` counts the folders cataloged by name.
`status.waiting` counts the titles a provider returned candidates for,
and a scan leaves those alone until a person names the right
`uniqueid` in the `.nfo`. `status.gaps` counts, per fact, the rows the
enricher still has to fill.
