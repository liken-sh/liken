---
title: The library bus
weight: 50
toc: true
---

# The library bus

No pod this operator creates holds an API credential. Every fact a
scanner records about a library, every choice a person makes on a
screen, and every position the progress store records reaches the
operator over the bus. The bus is the broker `media-operator` runs,
and this page is the contract of this operator's tree on it: every
topic, who writes it, who reads it, and the shape of each payload.
Your program can connect the same way, with a plain MQTT client and no
Kubernetes credentials. A media browser of another make that publishes
a play request gets the same service the project's browser gets.

## The topic base

Every topic extends one base, `liken/library` by default. The
operator reads the base from `LIBRARY_TOPIC_BASE` and passes it to
every pod it creates, so the whole tree moves together when a cluster
chooses another base. `media-operator`'s tree extends `liken/media`,
so the two operators' trees stay disjoint on the same broker. This page
writes topics without the base.

The rules that shape the tree are `media-operator`'s, and
[The media bus](https://media.liken.sh/docs/reference/bus/) states
them: state is retained and events are not, the topic names the object
and the payload does not, and a writer that leaves retained state
behind names an `availability` topic as its MQTT Last Will. This tree
follows the same three rules.

## The topics

| Topic | Writer | Readers | Retained | Payload |
|---|---|---|---|---|
| `libraries/{namespace}/{name}/status` | the catalog pod's reporter | the operator | yes | [the library report](#the-library-report) |
| `catalogs/{namespace}/availability` | the catalog pod's reporter | the operator | yes | `online` or `offline` |
| `players/{namespace}/{player}/play` | the media browser, or any client | the operator | no | [the play request](#the-play-request) |
| `plays/{namespace}/{play}/audience` | the operator | the progress role, the jellyfin role | yes | [the audience](#the-audience) |
| `plays/{namespace}/{play}/final` | the operator | the progress role, the jellyfin role | yes | [the final status](#the-final-status) |
| `plays/{namespace}/{play}/recorded` | the progress role | the operator | yes | [the recorded mark](#the-recorded-mark) |
| `plays/{namespace}/{name}/outside` | the jellyfin role | the progress role | no | [an outside play](#an-outside-play) |
| `people/{person}/forget` | the operator | every namespace's progress role | yes | [the forget request](#the-forget-request) |
| `people/{person}/forgotten/{namespace}` | the progress role | the operator | yes | [the forgotten answer](#the-forgotten-answer) |
| `progress/{namespace}/availability` | the progress role | nothing in this project | yes | `online` or `offline` |

The writers are the roles of this operator's pods. The reporter is
the container beside the standing catalog agent in the namespace's
catalog pod. The progress role is the container beside the standing
progress agent. The jellyfin role is the pod the operator stands
beside the progress store when the namespace's `Catalog` names a
server in [`spec.jellyfin`](/docs/reference/catalogs/#specjellyfin).
The operator itself holds the one API credential.

Every retained topic is cleared with an empty payload when the object
it names is gone, which is how MQTT drops a retained message, so a
client that connects later reads nothing for a `Library`, a `Play`, or
a `Person` that no longer exists. The operator clears the library,
play, and people topics. The progress role clears its own `forgotten`
answer when it reads an empty `forget`.

## The library report

`libraries/{namespace}/{name}/status`

The reporter publishes one report per `Library`, retained, and
rebuilds it whenever the catalog's runs table changes and while any
replicated table keeps changing. The operator folds each report into
that `Library`'s status, so the fields are the ones
[the `Library` status](/docs/reference/libraries/#status) carries. A
scan `Job` writes its `runs` row as its last catalog write and exits
only when this report names that `Job`, so a report is also the proof
that the catalog pod holds every row the `Job` wrote.

| Field | Type | Meaning |
|---|---|---|
| `titles` | integer | How many titles the catalog holds for this library. |
| `unidentified` | integer | How many folders the last walk could not identify. |
| `lastWalk` | RFC 3339 time | When the scan run last finished. |
| `lastChange` | RFC 3339 time | When the counts last moved. A report that counts the same rows as the one before carries the same time. A reporter that has just started takes `lastWalk`. |
| `items` | integer | The item rows the catalog holds for this library after the last walk pruned. |
| `files` | integer | The file rows the catalog holds for this library after the last walk pruned. |
| `walking` | boolean | True while a scan `Job` runs, which is a scan run whose start is later than its finish. |
| `removedLastSweep` | integer | How many rows the last full sweep removed. |
| `runs` | list | One entry per worker that has run against this library, sorted by worker. Absent until a worker has run. |
| `gaps` | map of integers | One count per fact of the rows that fact has left to fill. Absent when no fact has a gap. |
| `oldestAttempts` | map of RFC 3339 times | The oldest attempt this library holds for each fact. The operator reads it against `spec.refresh`. Absent when no fact has an attempt. |
| `waiting` | integer | How many titles the identity fact left as candidates for a person to choose from. |
| `unresolved` | integer | How many titles no provider could name. |
| `fights` | integer | How many titles a fact left because another writer holds the elements it writes. |

Each entry of `runs` carries `worker`, `job`, `started`, and
`finished`, plus `unidentified`, `removed`, and `failure` where the
run has them. `failure` is one sentence on why the run failed, and it
is empty for a run that finished its work.

    {
      "titles": 412,
      "unidentified": 9,
      "lastWalk": "2026-08-29T21:04:11Z",
      "lastChange": "2026-08-29T21:04:11Z",
      "items": 412,
      "files": 2189,
      "walking": false,
      "removedLastSweep": 3,
      "runs": [
        {"worker": "enrich", "job": "movies-enrich-29310751",
         "started": "2026-08-29T21:05:00Z", "finished": "2026-08-29T21:09:42Z"},
        {"worker": "scan", "job": "movies-scan-29310740",
         "started": "2026-08-29T21:03:02Z", "finished": "2026-08-29T21:04:11Z",
         "unidentified": 9, "removed": 3}
      ],
      "gaps": {"art": 4, "identity": 2},
      "oldestAttempts": {"identity": "2026-08-01T03:15:00Z"},
      "waiting": 1,
      "unresolved": 1,
      "fights": 0
    }

## The reporter's availability

`catalogs/{namespace}/availability`

`online` while the catalog pod's reporter runs. The reporter names
this topic as its MQTT Last Will with `offline` as the payload,
publishes `online` once it connects, and publishes `offline` as it
stops. Both are retained. A pod the kubelet killed reads `offline`,
and every `Library` of the namespace reads the phase `Offline` with
it. The reports the reporter left behind stand: the counts describe
the catalog, and they hold until the next report replaces them.

One reporter serves every `Library` of its namespace, so there is one
availability topic per namespace and none per `Library`.

## The play request

`players/{namespace}/{player}/play`

A play request is what a person chose on a screen. The browser holds
the catalog and no API credential, and the operator holds the
credential and no catalog, so the browser resolves the list of files
and publishes it here, and the operator joins each path to the
`Library`'s claim and creates the
[`Play`](https://media.liken.sh/docs/reference/plays/). The request is
an event and is not retained: a broker that held the last one would
replay it to the operator on every reconnect.

The operator names the topic on the browser container as
`LIBRARY_PLAY_TOPIC`. The `Player` comes from the topic and never from
the payload, so a request cannot name a `Player` other than the one
whose topic carried it. The operator creates a `Play` only for a
`Player` whose idle screen it holds, and only from a `Library` in that
`Player`'s namespace. A request that fails a check is reported in the
operator's log and dropped, because the screen has no way to answer.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `library` | string | yes | The `Library` the items come from, as `namespace/name`. The namespace must be the `Player`'s own. |
| `slug` | string | no | The catalog's slug for the item the person chose: the movie, or the chosen episode. The operator folds it into the `Play`'s name, so `kubectl get plays` reads as titles. A request with no slug names the `Play` after the `Player` alone. |
| `items` | list | yes | The files to play, in order. At least one. |
| `items[].path` | string | yes | The path of the item's main file, relative to the library root, exactly as the catalog stores it. An empty path, an absolute path, or one that climbs above the root is refused. |
| `items[].presentation` | object | no | How the item should look, in the `Play`'s own `spec.items[].presentation` field names: `type`, `hint`, `title`, `series`, `season`, `episode`, `episodeTitle`, `year`, `date`, `art`, and `trickplay`. `art` and `trickplay` are paths relative to the library root, and the operator joins them to the claim the way it joins `path`. |
| `people` | list of strings | no | Who is watching, as `Person` names. Each becomes an owner reference on the `Play`. A name the cluster does not hold is reported and dropped, and the `Play` still plays. |
| `aliases` | map of strings | no | The work's ids by provider, such as `{"tmdb": "1000001", "imdb": "tt0000001"}`. They become the `Play`'s `library.liken.sh/alias.{provider}` annotations, and they are the identity the progress store keys on. |
| `season` | integer | no | The season number of an episode. It becomes the `library.liken.sh/season` annotation. |
| `episode` | integer | no | The episode number of an episode. It becomes the `library.liken.sh/episode` annotation. |
| `start` | string | no | Where the first item begins, as a decimal count of seconds. It reaches the `Play`'s `spec.start` unchanged, because the player parses it and the operator does not. Omit it to start at the beginning. |
| `next` | object | no | The work that follows this one, which the player offers on its scrubber. It carries the three lines of the offer card, `reason`, `title`, and `detail`, spelled the way the continue-watching row spells them; `art`, a path relative to the root of `next.library`; `library`, the `namespace/name` of the next work's own `Library`, which may differ from `library` above; and `request`, an object the operator copies onto the `Play` unread. The operator joins `art` to the claim of `next.library` the way it joins an item's art, and the player publishes `request` back on the `Player`'s commands topic when a person takes the offer. |

The project's browser publishes one item, the film or the chosen
episode, leaves out every empty field, sends `start` only for a
resume, and sends `next` only where the page the person started from
names a natural successor: the next episode of a series, the next film
of a set, or the next member of a franchise. A series continues
through `next` and not through the item list, so every episode is its
own `Play` and its own row of progress.

    {
      "library": "den/movies",
      "slug": "a-quiet-harbor-2014",
      "items": [
        {
          "path": "A Quiet Harbor (2014)/A Quiet Harbor (2014).mkv",
          "presentation": {
            "type": "video",
            "hint": "movie",
            "title": "A Quiet Harbor",
            "year": 2014,
            "art": "A Quiet Harbor (2014)/poster.jpg",
            "trickplay": "A Quiet Harbor (2014)/A Quiet Harbor (2014).trickplay"
          }
        }
      ],
      "people": ["ada", "grace"],
      "aliases": {"tmdb": "1000001", "imdb": "tt0000001"},
      "start": "600"
    }

The `Play` the operator creates from that request carries one item
whose URI is `claim://`, the `Library`'s claim, `/`, and the library
root joined to the path. For a `Library` on the claim `movies` with
the root `/`, that is
`claim://movies//A Quiet Harbor (2014)/A Quiet Harbor (2014).mkv`.
The two people become owner references, the two aliases become
annotations, and `600` becomes `spec.start`.

## The audience

`plays/{namespace}/{play}/audience`

What the operator holds about a `Play` that the progress role cannot
read for itself. The operator publishes it retained as soon as the
`Play` exists, and again whenever it changes, so a progress role that
starts late reads it back from the broker. A `Play` written by
hand with the same annotations and owner references gets the same
audience.

| Field | Type | Meaning |
|---|---|---|
| `player` | string | The `Player` the `Play` runs on. |
| `library` | string | The `Library` the items came from, by name, or absent for a `Play` this operator did not create. |
| `people` | list of strings | The `Person` names on the `Play`'s owner references, or absent for a `Play` nobody claimed. |
| `aliases` | map of strings | The work's ids by provider, from the `Play`'s alias annotations. |
| `season`, `episode` | integers | The numbers of an episode, absent for a work that has none. |

    {
      "player": "den-tv",
      "library": "movies",
      "people": ["ada", "grace"],
      "aliases": {"tmdb": "1000001", "imdb": "tt0000001"}
    }

## The final status

`plays/{namespace}/{play}/final`

The `Play`'s last status, read off the API by the operator once the
`Play`'s phase is `Finished` or `Failed`, or once the `Play` is
deleting. Retained. It closes the gap the bus leaves: a progress role
that was down for the last report of a film still records where the
film ended. The positions are `H:MM:SS`, as the `Play` status carries
them.

    {"phase": "Finished", "item": 1, "position": "1:52:10", "duration": "1:52:10"}

## The recorded mark

`plays/{namespace}/{play}/recorded`

What the progress role wrote last for one `Play`, retained. The
operator holds a finalizer on every `Play` and releases it only when
`ended` is true, so a `Play` is never deleted before its last position
is in the store. `at` is the time of the write, RFC 3339 in UTC.

    {"item": 1, "position": "1:52:10", "ended": true, "at": "2026-08-29T23:01:14Z"}

## An outside play

`plays/{namespace}/{name}/outside`

One play that ran outside the cluster, on a Jellyfin server. The
jellyfin role publishes it from the webhook Jellyfin posts to, and
from a backfill of the server's history. The progress role records it
beside the cluster's own `Play`s. It is the one message in the tree
that is not retained: a progress role that was down for one post
catches the next, and a stop repeats the final position.

The `{name}` segment is not a `Play`. It is `jellyfin-{userId}-{itemId}`,
so one person's progress in one item is one row that moves, and a
rewatch moves it again.

| Field | Type | Meaning |
|---|---|---|
| `player` | string | `jellyfin`, in the column that names a `Player`. |
| `people` | list of strings | The people who watched, as `Person` names. |
| `aliases` | map of strings | The work's ids by provider. An episode carries the series' ids. |
| `season`, `episode` | integers | The numbers of an episode, and 0 for a work that has neither. |
| `position`, `duration` | integers | The position and the length, in seconds. |
| `ended` | boolean | True on a stop, which marks the row ended. |
| `at` | integer | The Unix time of the event, which the store writes as the recorded time. A newer `at` wins over what the row holds. |

    {
      "player": "jellyfin",
      "people": ["ada"],
      "aliases": {"tmdb": "1000001", "imdb": "tt0000001"},
      "season": 0,
      "episode": 0,
      "position": 1450,
      "duration": 6730,
      "ended": false,
      "at": 1756508474
    }

## The forget request

`people/{person}/forget`

The operator's request that one person's rows go, to every namespace's
progress role at once. A `Person` is cluster-scoped, so the topic has
no namespace. The operator holds a finalizer on every `Person`, and
when the `Person` is deleted it publishes this request retained with
the time it asked, so a store that starts later reads an ask it has
not answered. An empty payload is the clear, and every role clears its
own answer when it reads one.

    {"at": "2026-08-29T21:34:02Z"}

## The forgotten answer

`people/{person}/forgotten/{namespace}`

One namespace's answer that the person's rows are gone from its store,
retained, with the time of the sweep. The operator releases the
`Person` once every namespace that stands a store has answered, then
clears the request and every answer.

    {"at": "2026-08-29T21:34:05Z"}

## The progress role's availability

`progress/{namespace}/availability`

`online` while the namespace's progress role runs. The role names this
topic as its MQTT Last Will with `offline` as the payload, on the same
terms as the reporter's. Nothing in this project subscribes to it: the
operator reads the role's marks, and a mark that stops moving is the
signal it acts on. It is on the bus for a client that folds the store's
marks and needs to know whether their writer is gone.

## What this operator reads from the media tree

The progress role and the jellyfin role read two topics of
`media-operator`'s tree for every `Play` in their namespace, under
`liken/media` by default and under `LIBRARY_MEDIA_TOPIC_BASE` when that
operator's base moves: `plays/{namespace}/{play}/status`, the playback
pod's report with the position, and `plays/{namespace}/{play}/availability`,
its Last Will. The progress role joins the position with the audience
above into the rows it records. This operator writes nothing on the
media tree.

The media browser reads the `Player`'s own topics from the same tree,
which `media-operator` names in the `Player`'s `status.idle.bus`, and
[Put the media browser on a screen](/docs/guides/browser/#3-how-presses-reach-it)
describes what the browser does with each.
