# 99, Webhooks after a walk

Proposed and built on 2026-11-25. The operator's tests pass. No drill
has run on a cluster.

## The problem

Radarr and Sonarr post to a `Library`'s webhook when they import a
file, and the operator starts a walk of that folder. A person who also
wants a notification, such as "a new film is ready to watch", connects
the download manager to the notifier as well. That notification
arrives when the file lands, before the walk has read the file, and
before the phases have written its ids, its `.nfo` file, and its art.
The title is not on the wall yet.

The operator knows when a title is ready: the walk `Job`'s close
container waits for every phase and then for a catalog pod to confirm
the rows. Nothing outside the cluster can read that moment today. The
`ScanCompleted` `Event` carries only counts, and it is for a person
who runs `kubectl describe`.

## The contract

A `Library` names the addresses it posts to in `spec.webhooks`:

```yaml
spec:
  webhooks:
    - url: http://notifier.example.svc/library
      secretRef:
        name: notifier-token
        key: token
```

- `url` is required. The `Job`'s pod sends the request, so the address
  must be reachable from the `Library`'s namespace.
- `secretRef` is optional. The operator gives the key to the close
  container through a `secretKeyRef`, and the container sends it as
  `Authorization: Bearer <value>`. `key` defaults to `token`, as it
  does on a `MetadataProvider`.

After a walk `Job` adds or replaces a video, its close container sends
one `POST` to each address, with one JSON document that describes the
whole walk:

```json
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
      "change": "added",
      "table": "movies",
      "row": { "id": "movie:tmdb:1001", "title": "The Long Survey", "body": {}, "...": "..." },
      "files": [
        { "change": "added", "row": { "path": "...", "video_codec": "hevc", "...": "..." },
          "streams": [], "marks": [] }
      ],
      "genres": [], "credits": [], "trailers": [], "set": null, "franchises": []
    }
  ]
}
```

- `walk` is `full` for a walk of the whole library and `folders` for a
  walk of the folders that webhooks named.
- Each entry of `titles` is one movie or one series. `change` is
  `added` when the catalog had no video of the title before the walk,
  and `changed` when it had one.
- `row` is the title's row of the `movies` or `series` table, keyed by
  column name. The JSON columns `body` and `arts` are objects in the
  document, not strings.
- A movie has `files`. A series has `episodes`, the episodes whose
  videos changed, each with its own `row` and `files`. Each file has
  `change`, `added` or `replaced`, when the walk changed it, its `row`
  from `files`, and its `streams` and `marks` rows. A file that the
  walk did not change, such as a second encoding, has no `change`.
- `genres`, `credits`, and `trailers` are the title's rows of those
  tables. Each credit has its `row`, and its `contributor`, the row of
  the person it names. `set` is the row of the movie's set, and
  `franchises` lists each franchise `member` row that names one of the
  title's aliases, with the `franchise` row.

The rows are the catalog's columns, so the body is as wide as the
catalog, and a new column reaches a receiver with no change here. A
renamed column changes the document. Corrosion refuses to drop or
rename a column, so the catalog changes only by addition.

The close container sends the requests after its hand-off, so a
catalog pod holds every row in the body before the receiver reads it.
The trickplay worker can still be running.

## How the close container finds what changed

The walk can change a title's id: a folder with no provider id has an
id of the form `movie:path:<key>` until the identity phase writes the
provider's id and reads the folder again. So the change is found on
files, whose key is their path, and the ids are read only at the end.

1. Before it walks, the scan container reads the path, the size, and
   the modified time of every video of the library, and writes them to
   a note on the phases volume, the way it hands the stale partial
   files to the close container.
2. After the hand-off, the close container reads the same columns
   again. A path that is new is an `added` file. A path whose size or
   modified time differs is a `replaced` file.
3. It reads the items of each changed file through `file_items`, and
   the series of each episode, and builds one entry for each title.

A rename in the download manager moves a video to a new path. The walk
reads it as a new file, so the title is `changed` with an `added`
file.

The scan container writes the note only when its copy of the catalog
is synced, by the same wait the phases make (`enrichsync.go`). A copy
that has not synced reads as a library with no videos, and every title
would read as `added`. The scan container also writes no note on a
`Library`'s first walk, which has no confirmed run to sync to, because
that walk catalogs what the volume already held. A `Library` with no
webhooks writes no note.

## Delivery

The close container posts to every address at once, and retries each
one with a backoff of 1 second that doubles to 30 seconds, until the
address answers with a 2xx status or 2 minutes pass. A 4xx status
other than 408 and 429 ends the tries at once, because a retry cannot
change it. The `Job` holds the `Library`'s catalog
claim while it retries, so the next `Job` of the `Library` waits for
it, and the bound keeps that wait short. A request that does not
succeed in that time is not sent again.

The close container records the delivery as a runs row of a new
worker, `webhooks`, and hands it off as it does the run of the phases.
So `status.runs` shows when the last delivery finished, and its
`failure` names each address that refused, by its position in
`spec.webhooks` and its host. The operator posts a `WebhooksDelivered`
`Event` when that row finishes, or a `WebhooksFailed` warning when it
has a failure. A failed delivery does not fail the `Job`, because the
catalog holds the titles.

A log line names no title and no path, by the operator's rule for log
lines. The body goes only to the addresses the `Library` names.

## Set aside

- **A message on the library bus.** A retained topic holds one
  message, and an event topic loses what a disconnected client misses
  unless the client keeps a persistent session, which a broker that
  runs without persistence loses on a restart. A whole walk in one
  message can also be several megabytes, and one message per title
  needs a closing message so a receiver knows the walk is complete.
  One request per walk carries the whole walk, and the sender learns
  from the status whether the receiver took it.
- **A Kubernetes `Event` for each title.** The recorder in
  `kubernetes/events` limits a message to 1,024 bytes, drops an
  `Event` when its queue is full, and folds repeats into one. An
  `Event` is for a person who reads `kubectl describe`, by the rule in
  root plan 78, and a receiver needs every arrival.
- **An `Event` with the ids, and a receiver with its own catalog
  agent.** A catalog agent holds the whole namespace's catalog, about
  850 MB for 1,500 films and 200 series, and the operator creates
  every member of the cluster. A receiver of another make would need a
  membership that the operator does not manage.
- **A list that the scan container writes as it walks.** The ids that
  the walk reads are not final until identity has run, and the phases
  add the rows that a receiver wants. The close container reads the
  rows after the phases, so it reads the final ones.

## Proof

The operator's tests cover the comparison of the notes, the body of a
movie and of a series, the retry and its bound, the runs row, the
`Event`s, the `Job`'s environment and its `secretKeyRef`, and the first
walk that sends nothing. A drill on a cluster connects a `Library` to
a receiver, imports a film, and reads the body that the receiver got.
