---
name: webhooks
description: "Connect Radarr, Sonarr, and Jellyfin to a Library's webhook so an import rescans one folder at once, and have each walk post the titles it added or replaced to a receiver of your own. Use when new titles should appear on the wall in seconds instead of on the hourly walk, to rescan one folder by hand, or to send a notification when a new title is ready to watch."
---

This skill is the guide at https://liken.sh/library/docs/guides/webhooks/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Connect Radarr, Sonarr, and Jellyfin

A full walk runs on the `Library`'s schedule, once an hour by
default. A webhook rescans one folder as soon as the `Library` has no
other `Job` running, so a new title shows on the screens without waiting
for the next walk.

## The address

Every existing `Library` reports its webhook address:

    $ kubectl -n media get library movies -o jsonpath='{.status.webhook}'
    http://library-operator.liken-system.svc/webhook/media/movies

The address names the operator's own `Service` and the `Library`, so it
holds for as long as the `Library` exists. The `Service` is a
`ClusterIP`, reachable only inside the cluster, and the endpoint checks
nothing but the method and the path. Give it only to tools that run in
the same cluster.

## What a POST does

The operator reads the body for the path of what changed, in this
order: `movieFile.path`, `episodeFile.path`, `movie.folderPath`,
`movie.path`, `series.path`, then a top-level `Path`. The first one
found names the folder to rescan. A body with none of them, or no
body at all, schedules a full walk. Every POST answers `204 No
Content`.

The operator holds the folder and starts one walk `Job` for it on its
next pass. That `Job` walks the folder and runs every phase the
`Library`'s sources serve over it, so the title gets its id, its `.nfo`
file, its art, and its tiles in the same `Job`, as each phase before
them finishes the title.

An upgrade imports a new file under the old name, and the download
manager removes the old file's thumbnail and tiles. The rescan compares
the file's size with its probe record, finds a new file, and opens the
facts of that file: the probe, the marks, the tiles, and the thumbnail.
The file keeps its first arrival. The
[scanning guide](https://liken.sh/library/docs/guides/scanning/#a-file-replaced-at-the-same-path)
says what the walk opens and what it keeps.

A `Library` runs one `Job` at a time. A webhook that arrives while a
`Job` runs waits for it, and every folder the webhooks named in the
meantime goes into the next `Job`. So five imports during one run start
one `Job`, not five. The trailer files stop starting new titles fifteen
minutes into a run, so a `Job` that works through a backlog of them
ends within about fifteen minutes and one title, and the folders that
waited go next. Trickplay runs in a worker `Job` that runs no catalog
agent, outside the `Library`'s one `Job` at a time, so no folder waits
for a decode. Past 64 distinct folders held for one
`Library`, the whole set collapses to a full walk.

Each pod of the trickplay worker sends the same request when its video
is done, with the video's title folder in a top-level `path` field, so
the catalog reads the new tiles at once. The operator's log counts each one as a
webhook that named 1 folder.

## Radarr

Add a connection of type Webhook. Set the URL to the movies
`Library`'s address, and the method to POST. Enable the events that
name a file: import, upgrade, and rename. An event with no path, such
as the test, schedules a full walk. Saving a new connection runs the
test on its own, and the Test button runs it again, so a new
connection costs one full walk for each. Make the connection once and
let the walks finish before you test it by hand.

## Sonarr

The same, with the series `Library`'s address.

## Jellyfin

Install Jellyfin's Webhook plugin and add a generic destination with
the `Library`'s address. An item-added notification has a
top-level `Path`, which is what the operator reads.

## One writer beside the media

Radarr and Sonarr write beside the media too. This operator reads the
`uniqueid` in the Kodi `.nfo` they write, so keep that metadata
option on. Turn off their image writes, so that the art facts are the only
writers of images, and turn off their import of extra files. The [Jellyfin guide](https://liken.sh/library/docs/guides/jellyfin/#5-share-the-volume-with-jellyfin)
lists the Jellyfin switches.

Each file and directory that the operator writes into a title's folder
takes the owner and group of that folder. So Radarr and Sonarr can
change the operator's files the same way they change their own. When
Radarr upgrades a movie's file, it deletes `movie.nfo` and writes it
again, and a file that it may not write makes the import fail. After
Radarr writes the file, each fact fills only the groups that the file
no longer holds. The [enrichment guide](https://liken.sh/library/docs/guides/enrichment/#the-write-rule)
gives the rule. A run that may not change an owner, such as one on a
share that maps root to another user, writes each file as its own
user.

## Post each walk to a receiver

A walk `Job` can post the titles it added or replaced to addresses that
you name in `spec.webhooks`. The post arrives after the walk's phases
have written each title's ids, its `.nfo` file, and its art, and after
a catalog pod holds the rows. So a notifier that you connect here
sends "ready to watch" when the title is on the wall. A notifier that
Radarr or Sonarr calls directly sends it when the file lands, before
the walk.

    spec:
      webhooks:
        - url: http://notifier.example.svc/library
          secretRef:
            name: notifier-token
            key: token

The `Job`'s pod sends the request, so the address must be reachable
from the `Library`'s namespace. When you name a `secretRef`, the
request carries the value of that key as `Authorization: Bearer
<value>`. `key` defaults to `token`.

The close container of the walk `Job` sends one `POST` with a JSON
body for the whole walk:

    {
      "namespace": "media",
      "library": "movies",
      "kind": "movies",
      "job": "movies-walk-t3k1c9",
      "walk": "folders",
      "started": "2026-11-25T14:02:11Z",
      "finished": "2026-11-25T14:03:40Z",
      "titles": [
        {
          "change": "replaced",
          "table": "movies",
          "row": {"id": "movie:tmdb:1001", "title": "The Long Survey", "released": "1999", "body": {}},
          "files": [
            {"change": "added", "row": {"path": "The Long Survey (1999)/webdl.mkv", "height": 2160}, "streams": [], "marks": []}
          ],
          "previous": [
            {"row": {"path": "The Long Survey (1999)/bluray.mkv", "height": 1080}, "streams": [], "marks": []}
          ],
          "genres": [],
          "credits": [{"row": {"billing": 0, "name": "Ada Example", "part": "actor"}, "contributor": {}}],
          "trailers": [],
          "set": null,
          "franchises": []
        }
      ]
    }

- `walk` is `full` for a walk of the whole library, and `folders` for
  a walk of the folders that webhooks named. A walk of named folders
  that read the whole library, because a folder it was given is not on
  the volume, is `full`.
- Each entry of `titles` is one movie or one series. `row` is the
  title's row of the `movies` or `series` table, keyed by the catalog's
  column names, so it has every column the catalog has. `body` and
  `arts` are JSON values, not strings. A path is relative to the
  library's root.
- A movie has `change`, `files`, and `previous`. A series has
  `episodes`, with only the episodes to which a video arrived, and each
  episode has its own `change`, `row`, `files`, and `previous`.
- The `change` of a movie or an episode is one of these:
  - `added`: the catalog held no video of it before the walk.
  - `replaced`: the walk took a video away from it, because the video
    left or a new file was written at the same path, and a video
    arrived. An upgrade that writes the new file under a new name is
    `replaced`.
  - `version-added`: it kept every video it had, and another video
    arrived, such as a 2160p encoding beside a 1080p one.
- The `change` of a series is `added` when the catalog held no video of
  any of its episodes before the walk, and `changed` when it held one.
- `files` lists every file of the movie or the episode after the walk.
  A file has `change`, `added` for a new path or `replaced` for a path
  whose size or modified time changed, when the walk changed it, its
  `row`, and its `streams` and `marks`.
- `previous` lists the videos that the walk took away from the movie or
  the episode, with the values that the catalog held before the walk,
  so you can compare them with `files`. It is empty for `added` and
  `version-added`. An entry has the whole `row`, and its `streams` and
  `marks`, when the walk kept the video's rows before it walked. A walk
  of named folders keeps the rows of each video under the folders that
  are on the volume, up to 4 MB of rows for the whole walk. For every
  other video, `row` has only `path`, `size_bytes`, and `modified`, and
  `streams` and `marks` are `null`. That is a video outside the named
  folders, a video past the 4 MB, and each video of a walk that named
  no folders. A walk of named folders that read the whole library is
  `full`, and it still has the whole rows of the videos under the
  folders that were on the volume.
- `genres`, `credits`, and `trailers` are the title's rows of those
  tables. Each credit has its `row`, and its `contributor`, the row of
  the person, or `null` where the store has no entry. `set` is the row
  of the movie's set. Each entry of `franchises` has the franchise
  `member` row that names the title and the `franchise` row.

The change of an item comes only from the catalog's videos and the
items each video holds, and never from a file name. The walk can
change an item's id. When identity writes a provider id in place of
the folder's key, the operator finds the item's videos before the walk
through the item's aliases, and an episode through the aliases of its
series. Some changes of an id still read as `added`:

- The id changes from a provider id to the folder's key, such as
  `movie:tmdb:1001` to `movie:path:<key>`, and the video is renamed in
  the same walk. That happens when the `.nfo` file is gone and identity
  cannot write it again.
- The new `.nfo` file names other provider ids than the old one, and
  the folder is renamed in the same import.

A walk that runs after the old video left and before the new one
arrived takes the old video away and posts nothing, so the next walk
reads the new video as `added`.

A walk that adds or replaces no video posts nothing. The first walk of
a `Library` also posts nothing, because it catalogs what the volume
already held.

The close container retries each address that does not answer with a
2xx status. It waits 1 second before the first retry and doubles the
wait up to 30 seconds. It stops after 2 minutes, and when the receiver
answers with a 4xx status other than 408 or 429. The `Library`'s next
`Job` waits for those 2 minutes, because the `Job` that retries still
holds the catalog claim. A delivery that fails does not fail the
`Job`, and the close container does not send it again.

The last delivery is the `webhooks` entry of `status.runs`. Its
`failure` names each address that refused, by its position in
`spec.webhooks` and its host:

    $ kubectl -n media get library movies \
        -o jsonpath='{.status.runs[?(@.worker=="webhooks")]}'

The operator also posts a `WebhooksDelivered` `Event` on the `Library`
for each delivery, or a `WebhooksFailed` warning when an address
refused it.

## By hand

    curl -X POST http://library-operator.liken-system.svc/webhook/media/movies

A body naming a folder narrows the walk to that folder:

    curl -X POST http://library-operator.liken-system.svc/webhook/media/movies \
      -H 'Content-Type: application/json' \
      -d '{"movie":{"folderPath":"/media/movies/Example Movie (2019)"}}'
