# The Jellyfin backfill

Plan 50. A `Catalog` that names a Jellyfin server (plan 47) gets the
progress Jellyfin already holds, once. The operator runs one Job for
it, and the `Catalog`'s status says whether that Job has finished.

## The problem

Plan 47 carries progress both ways from the moment the jellyfin role
starts. Everything a person watched in Jellyfin before that moment
stays in Jellyfin alone. The store shows nothing to continue, and a
film half watched on a phone last month starts over on the
television.

## The facts this rests on

Read from Jellyfin's stable OpenAPI document, version 12.0.0, on
2026-09-08.

* Jellyfin holds one user-data row per user per item, and no history.
  The row carries `PlaybackPositionTicks`, `Played`, `PlayCount`, and
  `LastPlayedDate`. `LastPlayedDate` can be absent on an item a
  person marked played by hand.
* `GET /Items` takes a `userId`. With one, every item comes back with
  that user's `UserData`. `filters=IsResumable` narrows the answer to
  items with a resume point. `isPlayed=true` narrows it to items the
  user finished. The two are separate calls, because filters narrow
  together.
* The same call takes `includeItemTypes=Movie,Episode`, `recursive=true`,
  and `fields=ProviderIds`, the shape the index (plan 47) reads.
* The tie rule in the progress store, `recordOutside`, skips a
  message whose `at` is not newer than the row's `recorded`. A message
  for a row that does not exist always writes.

## The design

### The Job

The operator's image gains one more subcommand:

```
/library-operator jellyfin-backfill
```

It reads the same environment as the jellyfin role, minus the listen
address: the namespace, the bus address, the two topic bases,
`LIBRARY_JELLYFIN_URL`, and `LIBRARY_JELLYFIN_API_KEY`. It holds no
Kubernetes credential. It does this and exits:

1. Read the users with `GET /Users`.
2. For each user, read the resumable items and the played items, two
   calls, as movies and episodes with provider ids and user data.
3. Turn each item into one `outsidePlay` message, the same shape the
   webhook path publishes, on the same topic:
   `liken/library/plays/<namespace>/jellyfin-<user id>-<item id>/outside`.
   An episode takes its series' provider ids, read the way the webhook
   path reads them, with the season and episode numbers beside them.
   `position` is the ticks in seconds. `ended` is `Played`. `at` is
   `LastPlayedDate` as Unix time, or 1 when the date is absent, so
   any real play wins over it.
4. Skip an item with no provider ids, and count it.
5. Exit zero, with one log line of counts: users, items published,
   items skipped.

Every message is not retained, as in plan 47. The Job runs only once
the progress pods are Ready, so the store is listening. A message the
store misses is the one thing a rerun of the Job answers.

The Job is `<catalog>-jellyfin-backfill`, owned by the `Catalog`,
labeled the way every worker Job is, with the `Catalog`'s name in
the library label and `jellyfin-backfill` as the worker. It has the
same backoff limit and TTL as a scan Job. Its pod is the jellyfin
role's container with the different subcommand, no port, and no
readiness probe.

### The operator stands it

The catalog pass gains one step after the jellyfin pair stands. It
reads the `Catalog`'s status and the worker Jobs of the pass:

* The status says the backfill finished against this server, and the
  Job is absent: nothing to do.
* The status does not say so, and no Job stands: create the Job.
* The Job stands and is active: wait.
* The Job succeeded: write the status, and let the TTL remove the Job.
* The Job failed past its backoff: delete it, so the next pass creates
  it again, with the cleanup Job's recreate backoff.

A `Catalog` that names no Jellyfin server gets no Job and keeps its
status. A `Catalog` whose `spec.jellyfin.url` changes runs the backfill
again, because the status names the server it backfilled from.

### The status

`status.jellyfin` is new on the `Catalog`:

```yaml
status:
  jellyfin:
    server: http://jellyfin.jellyfin.svc:8096
    backfill: Finished
    backfilled: "2026-09-08T14:02:11Z"
```

`backfill` is one of `Pending`, `Running`, `Failed`, and `Finished`.
`server` is the URL the backfill ran against. `backfilled` is the
time the Job succeeded, and is absent until it did. A `Catalog` that
names no Jellyfin server has no `status.jellyfin`.

The Job's counts stay in its log. The status answers one question,
whether the backfill finished, which is what a person checks.

### The manual

The Jellyfin guide gains a short section: the backfill runs once, on
its own, what it carries and what it cannot, that a rerun is safe,
and how to make it run again (clear `status.jellyfin` or change the
URL).

## What it does not do

* It carries no history. Jellyfin holds one state per item, so the
  store gets the last position and whether the person finished the
  item, and nothing about earlier viewings. `PlayCount` has no home
  in the store.
* It makes no Person. A Jellyfin user with no `Person` of the same
  name still publishes, and the row names that user as its person,
  the way the webhook path does.
* It writes nothing to Jellyfin. The outbound half of plan 47 answers
  Play status on the media tree, and a store row is not that.

## The drill

On the house, with `status.jellyfin` absent:

1. Roll the release. Watch the Job stand and finish.
2. Read the Catalog's status: `backfill: Finished` with a time.
3. Query the store for rows with `player = 'jellyfin'`, and compare
   a few against Jellyfin's continue-watching row.
4. Open the browser and see a film Jellyfin held resume at Jellyfin's
   position.
