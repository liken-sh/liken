---
name: scanning
description: "How a scan walks a library's root into the catalog: what it reads, when it runs, how mark and sweep removes what is gone, and what deleting a Library does. Use when titles are missing from the wall, when a scan seems stuck, or before deleting a Library."
---

This skill is the guide at https://library.liken.sh/docs/guides/scanning/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

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
`.nfo` file and no year is counted in `status.unidentified` and cataloged
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

A folder with one of those names that holds video files is an extras
folder wherever it is, at the library root or inside a grouping folder.
The scanner reads no title from it, and nothing under it is cataloged.
A folder with one of those names that holds only folders is a grouping
folder. So a genre folder named `Shorts` is read, and the titles under
it are cataloged. A title whose own name is one of those words has its
year in its folder name, `Trailers (2016)`, which is not the bare
word.

Every file gets a row. The scanner classifies each one as `video`,
`audio`, `subtitle`, `image`, `metadata`, `trickplay`, or `other`
from its name, its folder, and one `stat`. It opens no file to
classify it. A subtitle's language is the tag in its name, and `hi`
after a language tag marks it hearing-impaired. Dot-named entries,
`Thumbs.db`, `desktop.ini`, and the trash and service directories of
common NAS systems are skipped.

### .nfo files and art

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

Beside a title, a dot-named directory holds the data that the `.nfo` file has
no element for: one YAML file per fact, named for the fact. `identity.yaml`
holds the provider ids, or the candidates left for a person to choose
from. `arrival.yaml` holds when each video file was first seen. Every
other `<fact>.yaml` holds what that fact wrote, which provider answered,
and its attempts. One file per writer lets the phases of a `Job` run at
once on a network mount with no locks. The scan reads these files and
never writes them.

### `.contributors/`

At the library root, one directory per credited person, sharded by
the first two characters of the person's slug. Each holds
`contributor.yaml` with the name and the provider ids, and, once the
contributors phase fills them, `biography.txt` and `headshot.jpg`. The walk
reads this directory after the titles. It is the one dot-named
directory the walk enters. An entry whose `contributor.yaml` holds
only `mergedInto` is one that a merge of two entries removed. The walk
records the merge and no person for it. The
[enrichment guide](https://library.liken.sh/docs/guides/enrichment/#people) describes the
merge.

## When a scan runs

Every scan runs in a `Job` the operator creates for the `Library`, and
only one `Job` of a `Library` runs at a time. The operator starts a
walk `Job` when one of these asks for it and no other `Job` of the
`Library` is unfinished:

* `spec.scan.schedule`, a cron expression in the form a `CronJob`
  takes, once an hour by default. It is in UTC unless it starts with a
  `CRON_TZ=` prefix. A walk is due when a time in the
  schedule has passed since the last full walk started. A `Library`
  that has never been walked is due at once.
* A request in `spec.refresh.scan`. `kubectl liken library rescan
  movies` writes the current time there. A walk that starts at or
  after the time answers the request, so asking again is a matter of
  setting a later time. It is the same map the
  [enrichment guide](https://library.liken.sh/docs/guides/enrichment/) uses for facts.
* A [webhook](https://library.liken.sh/docs/guides/webhooks/). The walk reads only the folders
  the webhooks named, and a webhook that named no folder asks for a
  full walk.

A walk that is due while another `Job` of the `Library` runs waits for
that `Job` to finish, and the folders webhooks name in the meantime
wait with it. The next `Job` walks all of them.

    kubectl -n media get jobs -l library.liken.sh/library=movies,library.liken.sh/worker=walk

A walk `Job` is named `<library>-walk-<suffix>`. It runs the `scan`
container beside every phase the `Library`'s sources serve: the probe,
the arrival fact, identity, the `.nfo` facts, the art, the trailers,
the marks, the people, trickplay, and the trailer files. All of them
start together, and each phase works on a title as soon as the walk and
the phases before it have written that title's rows. The
[enrichment guide](https://library.liken.sh/docs/guides/enrichment/#3-what-the-phases-do)
describes the phases. The `scan` container runs a person's own image
when the kind's settings block names one, and it always mounts the
library volume read-only.

The walk writes a `runs` row under the `scan` worker when it starts,
and again when it finishes. A walk of folders writes its row under the
`rescan` worker, so the full walk's counts stay beside it. The `Job`'s
`close` container writes the `enrich` row after the last phase and
waits until a catalog pod confirms it. So a `Job` that completed is a
`Job` whose rows reached a durable copy of the catalog.

Every `Job` of a `Library` runs its catalog agent on the `Library`'s one
catalog claim, `<library>-catalog`. The operator's rule of one `Job` at
a time is what keeps two agents off one database. On a per-node class
the claim is also `ReadWriteOncePod`, so the scheduler keeps a second
pod of the claim `Pending` while the first one runs.

## A Job that does not start or that fails

Only one `Job` of a `Library` runs at a time, so a `Job` whose pod
cannot start holds back every walk and every phase of that `Library`.
The `Library`'s status names such a `Job`. When the pod of a `Job`
stays `Pending` for five minutes, the phase is `Blocked`, and the
`Ready` condition is `False` with the reason `JobNotStarted`:

    $ kubectl -n media get libraries
    NAME         KIND         TITLES   ITEMS   FILES   WAITING   SOURCES   STATUS    READY   AGE
    franchises   franchises   35       35      0       0                   Blocked   False   19d

    $ kubectl -n media get library franchises -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
    the pod franchises-walk-dlov5hvq2ryn-gklk6 of the Job franchises-walk-dlov5hvq2ryn has not started: GitVolumeRefused: readOnly: a claim on this driver has to be mounted read-only; set readOnly: true on the pod's persistentVolumeClaim volume

The text after the `Job`'s name is from Kubernetes. When no node can
take the pod, it is the scheduler's reason, `Unschedulable`, and its
sentence. When a node took the pod, it is the newest `Warning` event
about the pod, for example a volume that a CSI driver refused or an
image that the kubelet cannot pull. `kubectl describe pod` shows every
event of the pod.

Repair what the message names. A `Job` keeps the pod spec it was
created with, so when the repair is in the `Library` or in the
operator, delete the `Job`. The next pass then creates the `Job` that
is due from the `Library` as it is now:

    kubectl -n media delete job franchises-walk-dlov5hvq2ryn

Every `Job` of a `Library` has a deadline of two hours in
`activeDeadlineSeconds`, and the time its pod stays `Pending` counts.
The longest healthy `Job` is shorter: trickplay and the trailer files
start no title after 15 minutes, and one trickplay title decodes for at
most an hour. At the deadline, Kubernetes fails the `Job` with the
reason `DeadlineExceeded`, so a `Job` whose pod never starts holds
back the next `Job` of the `Library` for at most two hours.

A `Job` also fails when three of its pods fail, with the reason
`BackoffLimitExceeded`. After a `Job` fails, the phase is `Failed`,
and `Ready` is `False` with the reason `JobFailed`, until a later `Job`
of the `Library` succeeds. The message names the `Job` and the reason.
The operator starts the next `Job` at once after the first failure.
After each failure that follows, it waits 10 seconds, and it doubles
the wait up to 5 minutes. A `Job` that succeeds resets the wait. A
failed `Job` stays for an hour, so its logs can be read:

    kubectl -n media logs job/franchises-walk-dlov5hvq2ryn --all-containers

## Mark and sweep

Each full walk has an epoch. The walk marks every id, path, and link
it reads with that epoch. A prune pass then deletes every row of this
library the epoch did not mark, in batches of five hundred.

Two guards keep a bad walk from emptying a library. A walk that could
not read every directory, or that found less than half of what the
catalog holds, is incomplete. It writes what it read and prunes
nothing, and the log reports it:

    incomplete walk: could not read the whole volume, keeping the last counts

A prune whose epoch marked nothing at all is refused as an error.
`status.removedLastSweep` reports what the last sweep removed, so a
mass delete is visible without a shell.

The walk itself runs eight workers over a shared pool of directories.
That keeps a network volume busy without a burst large enough to slow
a player.

## Deleting a Library

A `Library` has a finalizer, and deleting it starts a departure that
removes its rows from the namespace's catalog. The operator starts no
new `Job` for a deleting `Library`, and it waits for any `Job` of the
`Library` to finish. Then it runs a cleanup `Job` named
`<library>-cleanup` on the `Library`'s catalog claim, which deletes the
rows in batches through its own catalog agent. The finalizer clears
once the cleanup `Job` succeeded and a catalog pod confirmed its run.

While this runs, the phase is `Departing`, and the `Departing`
condition names the step: `ScanRunning` while a walk `Job` runs,
`EnrichRunning` while another `Job` of the `Library` runs, `Sweeping`,
`AwaitingEcho`, or `Blocked` when the cleanup `Job` keeps failing or
the namespace holds two `Catalogs`. There is no timeout. The operator
reports the blocker for as long as the object is deleting.

A namespace with no `Catalog` releases at once, because nothing there
holds the rows. A library whose own catalog claim is already gone gets
a fresh, empty one for the cleanup `Job`. That `Job`'s agent receives
the rows over gossip and then sweeps them.

## Reading progress

    kubectl -n media get library movies -o jsonpath='{.status.phase} titles={.status.titles} unidentified={.status.unidentified} waiting={.status.waiting} gaps={.status.gaps}{"\n"}'
    kubectl -n media logs -l library.liken.sh/library=movies,library.liken.sh/worker=walk -c scan --tail=100

A finished walk logs its counts:

    walk complete: 128 titles from 131 folders, 3 unidentified, 0 removed, in 41s

`status.unidentified` counts the folders cataloged by name.
`status.waiting` counts the titles a provider returned candidates for,
and the identity phase does not retry those until a person names the
right `uniqueid` in the `.nfo`. `status.gaps` counts, per fact, the
rows the phases still have to fill.
