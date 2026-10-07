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
[The media bus](https://liken.sh/media/docs/reference/bus/) states
them: state is retained and events are not, the topic names the object
and the payload does not, and a writer that leaves retained state
behind names an `availability` topic as its MQTT Last Will. This tree
follows the same three rules, with one exception:
[who is watching](#who-is-watching) names no Last Will, because that
answer is meant to outlive the process that wrote it.

## The topics

| Topic | Writer | Readers | Retained | Payload |
|---|---|---|---|---|
| `libraries/{namespace}/{name}/status` | the catalog pod's reporter | the operator | yes | [the library report](#the-library-report) |
| `catalogs/{namespace}/availability` | the catalog pod's reporter | the operator | yes | `online` or `offline` |
| `libraries/{namespace}/{name}/refresh` | the operator | the catalog pod's reporter | yes | [the refresh times](#the-refresh-times) |
| `players/{namespace}/{player}/play` | the media browser, or any client | the operator | no | [the play request](#the-play-request) |
| `players/{namespace}/{player}/audience` | the media browser | the media browser, and any client that wants to know who is in the room | yes | [who is watching](#who-is-watching) |
| `plays/{namespace}/{play}/audience` | the operator | the progress role, the jellyfin role | yes | [the audience](#the-audience) |
| `plays/{namespace}/{play}/final` | the operator | the progress role, the jellyfin role | yes | [the final status](#the-final-status) |
| `plays/{namespace}/{play}/recorded` | the progress role | the operator | yes | [the recorded mark](#the-recorded-mark) |
| `plays/{namespace}/{name}/outside` | the jellyfin role | the progress role | no | [an outside play](#an-outside-play) |
| `plays/{namespace}/{name}/mark` | the media browser | the progress role, the jellyfin role | yes | [a mark](#a-mark) |
| `plays/{namespace}/{name}/sent` | the jellyfin role | the jellyfin role | yes | [a sent mark](#a-sent-mark) |
| `people/{person}/forget` | the operator | every namespace's progress role | yes | [the forget request](#the-forget-request) |
| `people/{person}/forgotten/{namespace}` | the progress role | the operator | yes | [the forgotten answer](#the-forgotten-answer) |
| `progress/{namespace}/availability` | the progress role | the jellyfin role | yes | `online` or `offline` |

The writers are the roles of this operator's pods. The reporter is
the container beside the standing catalog agent in the namespace's
catalog pod. The progress role is the container beside the standing
progress agent. The jellyfin role is the pod the operator runs
beside the progress store when the namespace's `Catalog` names a
server in [`spec.jellyfin`](/docs/reference/catalogs/#specjellyfin).
The operator itself holds the one API credential.

Every retained topic is cleared with an empty payload when the object
it names is gone, which is how MQTT drops a retained message, so a
client that connects later reads nothing for a `Library`, a `Play`, or
a `Person` that no longer exists. The operator clears the library,
play, and people topics. The progress role clears its own `forgotten`
answer when it reads an empty `forget`. The media browser clears its
own audience topic when the answer on it lapses. The progress role
clears each mark once it has recorded it and the retention has run,
and the jellyfin role clears its record of a sent mark with the mark.

## The library report

`libraries/{namespace}/{name}/status`

The reporter publishes one report per `Library`, retained, and
rebuilds it whenever the catalog's runs table changes and while any
replicated table keeps changing. The operator folds each report into
that `Library`'s status, so the fields are the ones
[the `Library` status](/docs/reference/libraries/#status) carries. The
close container of a `Library`'s `Job` writes the `enrich` run as the
`Job`'s last catalog write and exits only when a catalog pod confirms
it, so a run in this report with a finish is also the proof that a
catalog pod holds every row the `Job` wrote.

| Field | Type | Meaning |
|---|---|---|
| `titles` | integer | How many titles the catalog holds for this library. |
| `unidentified` | integer | How many folders the last walk could not identify. |
| `lastWalk` | RFC 3339 time | When the scan run last finished. |
| `lastChange` | RFC 3339 time | When the counts last moved. A report that counts the same rows as the one before carries the same time. A reporter that has just started takes `lastWalk`. |
| `items` | integer | The item rows the catalog holds for this library after the last walk pruned. |
| `files` | integer | The file rows the catalog holds for this library after the last walk pruned. |
| `walking` | boolean | True while a walk runs, which is a scan run whose start is later than its finish. |
| `removedLastSweep` | integer | How many rows the last full sweep removed. |
| `runs` | list | One entry per worker that has run against this library, sorted by worker. Absent until a worker has run. |
| `gaps` | map of integers | One count per fact of the rows that fact has left to fill, counted with the `Library`'s [refresh times](#the-refresh-times). Absent when no fact has a gap. |
| `oldestAttempts` | map of RFC 3339 times | The oldest attempt this library holds for each fact. The operator reads it against `spec.refresh`. Absent when no fact has an attempt. |
| `waiting` | integer | How many titles the identity fact left as candidates for a person to choose from. |
| `unresolved` | integer | How many titles no provider could name. |
| `fights` | integer | How many titles a fact left because another writer holds the elements it writes. |

Each entry of `runs` carries `worker`, `job`, `started`, and
`finished`, plus `unidentified`, `removed`, and `failure` where the
run has them. `failure` is one sentence on why the run failed, and it
is empty for a run that finished its work.

    {
      "titles": 128,
      "unidentified": 9,
      "lastWalk": "2026-08-29T21:04:11Z",
      "lastChange": "2026-08-29T21:04:11Z",
      "items": 128,
      "files": 560,
      "walking": false,
      "removedLastSweep": 3,
      "runs": [
        {"worker": "enrich", "job": "movies-walk-dlcg1z2yyzgw",
         "started": "2026-08-29T21:03:01Z", "finished": "2026-08-29T21:09:42Z"},
        {"worker": "scan", "job": "movies-walk-dlcg1z2yyzgw",
         "started": "2026-08-29T21:03:02Z", "finished": "2026-08-29T21:04:11Z",
         "unidentified": 9, "removed": 3}
      ],
      "gaps": {"art": 4, "identity": 2},
      "oldestAttempts": {"identity": "2026-08-01T03:15:00Z"},
      "waiting": 1,
      "unresolved": 1,
      "fights": 0
    }

## The refresh times

`libraries/{namespace}/{name}/refresh`

The reporter counts each gap with the `Library`'s
[`spec.refresh`](/docs/reference/libraries/#spec--refresh), so a title a
refresh reopened is in `gaps`. The reporter holds no API credential and
cannot read the `Library`, so the operator publishes the refresh time of
each fact the `Library` names, retained, whenever the times change. The
reporter subscribes to the refresh topics of its own namespace and
publishes the report again when one changes, so an edit of `spec.refresh`
reaches `gaps` with no restart of the catalog pod. The payload is the value
a `Job`'s containers read from `LIBRARY_REFRESH`: one RFC 3339 time per
fact. The walk is not a fact, and it is not in the payload. A `Library`
that names no refresh time publishes an empty object.

    {"credits": "2026-09-30T00:00:00Z", "appearances": "2026-09-30T00:00:00Z"}

A report that the reporter built before the refresh times reached it
counts with none. The operator reads `oldestAttempts` against
`spec.refresh` as well, so a fact with a refresh that has work left starts
its work from such a report too.

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
[`Play`](https://liken.sh/media/docs/reference/plays/). The request is
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
| `items[].presentation` | object | no | How the item should look, in the `Play`'s own `spec.items[].presentation` field names: `type`, `hint`, `title`, `series`, `season`, `episode`, `episodeTitle`, `year`, `date`, `art`, `trickplay`, `role`, `appearances`, `contributors`, and `marks`. `role` says what the item is to the work it presents: `trailer`, or empty for the work itself. `appearances` is the path of the spans file that the appearances fact wrote for the item's file, and `contributors` is the path of the library's `.contributors` directory, which the spans file names each portrait under. `marks` is a list of spans of the item's file, each with a `kind`, a `start` and an `end` in seconds from the start of the file, and a `source`, the provider block the span came from. An absent `start` is the start of the file, and an absent `end` is the end of the file. The operator carries `marks` unread, and the display merges the candidates. `art`, `trickplay`, `appearances`, and `contributors` are paths relative to the library root, and the operator joins them to the claim the way it joins `path`. |
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
whose URI is `claim://`, the `Library`'s namespace, `/`, the
`Library`'s claim, `/`, and the library root joined to the path. The
root is an absolute path, so the URI has two slashes after the claim.
For a `Library` in the namespace `den` on the claim `movies` with the
root `/`, that is
`claim://den/movies//A Quiet Harbor (2014)/A Quiet Harbor (2014).mkv`.
The two people become owner references, the two aliases become
annotations, and `600` becomes `spec.start`.

## Who is watching

`players/{namespace}/{player}/audience`

The room on one screen, as the browser asked a person for it. The
browser is both the writer and the reader: it publishes the answer
retained, and it reads the broker's catch-up back when it starts. A
screen pod that restarts inside the idle window draws the room it had
and asks nobody. The operator names the topic on the browser container
as `LIBRARY_AUDIENCE_TOPIC`, and reads none of it.

| Field | Type | Meaning |
|---|---|---|
| `people` | list of objects | Who is watching, in the order the answer named them. Each entry carries `name`, the `Person` every record keys on, and `displayName`, the name the screen draws. An empty list is the answer "nobody", which is an answer and not the absence of one. |
| `at` | integer | The Unix time of the last press, in whole seconds. |

    {"people": [{"name": "person-a", "displayName": "Person A"}], "at": 1757350000}

The browser writes the message when a person answers the picker, and
at most once a minute while a person presses keys. The stamp is what
ages, and a second either way changes nothing a reader can act on. The
browser republishes what it holds whenever its bus session starts,
under the stamp the message already carries, because a reconnect is
not a press. When the answer lapses, three hours after the last press,
the browser publishes the empty payload, which drops the retained
message.

The browser acts on a retained delivery alone, which is the broker's
catch-up on the subscription. A live delivery is the echo of its own
write and changes nothing. It takes the answer where `at` is inside
the idle window, and it asks again where the stamp is older. A name
the cluster's `Person` list does not hold is dropped.

This topic names no availability topic as its Last Will, and that is
deliberate. The answer describes the room, not the browser, so it must
outlive the browser's process. A pod the kubelet restarts reads the
room back.

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
| `credits` | list of objects | Every `credits` mark of the `Play`'s first item, from its presentation: `start` and `end` in seconds, each absent where the mark runs from the start or to the end of the file. Absent where the item has none. The jellyfin role reads it for the watched rule. |

    {
      "player": "den",
      "library": "movies",
      "people": ["ada", "grace"],
      "aliases": {"tmdb": "1000001", "imdb": "tt0000001"},
      "credits": [{"start": 6204.5, "end": 6600}, {"start": 6210}]
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

The mark never goes back to `ended: false`. Once the row is ended, the
progress role writes nothing more for that `Play` and publishes no new
mark, so a position report that reaches the bus after the final leaves
the row and the mark where the final put them.

    {"item": 1, "position": "1:52:10", "ended": true, "at": "2026-08-29T23:01:14Z"}

## An outside play

`plays/{namespace}/{name}/outside`

One play that ran outside the cluster, on a Jellyfin server. The
jellyfin role publishes it from the webhook Jellyfin posts to, from the
reconcile it runs each time the progress role comes online, and from a
backfill of the server's history. The progress role records it
beside the cluster's own `Play`s. It is the one message in the tree
that is not retained: a progress role that was down for one post
catches the next, and a stop repeats the final position.

The `{name}` segment is not a `Play`. It is `jellyfin-{userId}-{itemId}`,
so one person's progress in one item is one row that moves, and a
rewatch moves it again.

The two ids are Jellyfin Guids, in the spelling its API writes: 32
hexadecimal digits with no dashes. The webhook, the reconcile, and the
backfill all write that spelling, so the three name one row.

| Field | Type | Meaning |
|---|---|---|
| `player` | string | `jellyfin`, in the column that names a `Player`. |
| `people` | list of strings | The people who watched, as `Person` names. |
| `aliases` | map of strings | The work's ids by provider. An episode carries the series' ids. |
| `season`, `episode` | integers | The numbers of an episode, and 0 for a work that has neither. |
| `position`, `duration` | integers | The position and the length, in seconds. |
| `ended` | boolean | True on a stop, which marks the row ended. |
| `at` | integer | The Unix time of the event, which the store writes as the recorded time. A newer `at` wins over what the row holds. |

A work Jellyfin reports as played carries a `position` at the end of
the work, whatever resume point Jellyfin holds. A mark a person set by
hand has no position of its own.

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

## A mark

`plays/{namespace}/{name}/mark`

One mark a person set on one title at the media browser: watched, or
cleared. It applies to everyone at the screen. The progress role
records it as one row of the store, beside the cluster's own `Play`s.
The jellyfin role reads the same message to send the mark to Jellyfin.
A mark can also name a list of a series' episodes, which
[Pick up here](/docs/guides/browser/#6-marking-a-title-watched-or-clearing-it)
publishes: see [a list of episodes](#a-list-of-episodes).

The mark is retained, because the person pressed once and nothing
repeats the press. A progress role that is down when the person
presses reads the mark when it subscribes again. The progress role
clears the topic 24 hours after `at`, once it has recorded the mark.
A mark it reads after those 24 hours, it records and clears at once.
A payload that is not a mark, it clears at once and records nothing.
Every reader receives a retained mark again on each subscription
within the 24 hours, so a reader must treat a second delivery as the
same mark.

The `{name}` segment is not a `Play`. It is `mark-{player}-{at}`, so
each press is one topic and one row. The browser moves a second press
within the same second to the next second, so no two marks of one
screen share a name. The progress role writes a row only when the
store holds no row of that name recorded at `at` or later, so a mark
delivered again never changes a row.

The browser takes the branch of the tree from `LIBRARY_PLAYS_TOPIC`,
`{base}/plays/{namespace}`, which the operator sets on the browser
container, and the `Player`'s name from `MEDIA_PLAYER_NAME`.

| Field | Type | Meaning |
|---|---|---|
| `mark` | string | `watched` or `cleared`. |
| `player` | string | The `Player` whose screen the person pressed on, in the column that names a `Player`. |
| `people` | list of strings | The people at the screen, as `Person` names. Empty where nobody answered who is watching. |
| `aliases` | map of strings | The work's ids by provider, as a play request names them. An episode carries the series' ids. |
| `season`, `episode` | integers | The numbers of an episode, and 0 for a work that has neither. |
| `position` | integer | The duration for `watched`, and 0 for `cleared`, in seconds. |
| `duration` | integer | The length of the work in seconds: the duration of the audience's play of the work where one exists, and the catalog's running time where none does. |
| `at` | integer | The Unix time of the press, which the store writes as the recorded time. The row stands over every play recorded before it, and a later play replaces it. |

The row is ended at `at`, with the phase `Finished`, because no `Play`
runs behind it.

A mark that names one work carries no `episodes` field.

    {
      "mark": "watched",
      "player": "living-room",
      "people": ["ada"],
      "aliases": {"tvdb": "1000002"},
      "season": 2,
      "episode": 5,
      "position": 2760,
      "duration": 2760,
      "at": 1759140000
    }

### A list of episodes

Pick up here marks every earlier episode of a series watched and clears
every later episode the people at the screen started, in one press. The
browser publishes one mark for the whole press, on the same topic and
retained the same way, and the mark lists the episodes in `episodes`.
The series' aliases, the people, the `Player`, and `at` are stated once.
The mark carries no `season`, `episode`, `position`, or `duration` of
its own: each entry of the list states its own.

| Field | Type | Meaning |
|---|---|---|
| `episodes` | list of objects | The episodes the mark covers, in series order. Each entry has `mark`, `season`, `episode`, `position`, and `duration`, with the meaning each field has on a mark of one work. |

An entry's `mark` is `watched` or `cleared`, and an entry with no
`mark` takes the message's. The browser states `mark` on every entry,
and states `watched` on the message. A message whose `mark`, or whose
entry's `mark`, is neither of the two is no mark: the progress role
clears it and records nothing.

The progress role writes one row for each entry. A watched row is named
`{name}-s{season}e{episode}` with each number in four digits, as in
`mark-living-room-1759140000-s0001e0004`. A cleared row is named
`{name}-cleared-s{season}e{episode}`, as in
`mark-living-room-1759140000-cleared-s0003e0002`. Every row is recorded
at `at`. The browser breaks a tie in recorded time on the row's name.
The four digits make the watched names sort in series order, and every
cleared name sorts before every watched name, so the browser reads the
last watched episode of the list as the newest, and never a later
episode the press cleared. A cleared row stands at position 0, and a
thread that stood on it would offer nothing. The role writes each
row only when the store holds no row of that name recorded at `at` or
later, so a list delivered again writes only the rows that are missing.
It clears the topic once, as it clears a mark of one work.

The jellyfin role writes each entry to each person at the screen, with
the entry's own mark, and publishes one [sent mark](#a-sent-mark) for
the whole list once every entry has landed. An entry Jellyfin does not
hold is done with no write.

The press publishes one message, and not one mark for each episode,
because each mark's name takes the second of its press. The browser
moves a second press within the same second to the next second, so a
mark for each of 100 episodes would carry an `at` up to 100 seconds in
the future. The play the same press starts would read as older than
those marks until that time had passed.

    {
      "mark": "watched",
      "player": "living-room",
      "people": ["ada"],
      "aliases": {"tvdb": "1000002"},
      "episodes": [
        {"mark": "watched", "season": 1, "episode": 4, "position": 2760, "duration": 2760},
        {"mark": "watched", "season": 2, "episode": 1, "position": 2700, "duration": 2700},
        {"mark": "cleared", "season": 3, "episode": 2, "position": 0, "duration": 2760}
      ],
      "at": 1759140000
    }

## A sent mark

`plays/{namespace}/{name}/sent`

The jellyfin role's record that it sent one mark to Jellyfin, under the
mark's own name. The role publishes it retained once the mark's write
lands for every person Jellyfin holds, and sends no mark whose record it
reads. A jellyfin role that restarts within the mark's 24 hours reads
both back, so it does not send the mark again over a toggle a person
made in Jellyfin since. Jellyfin clears an item's last played date on an
unplayed toggle, so the date cannot stop a second send alone.

The role clears the record when the progress role clears the mark. A
record it reads more than 24 hours after `at`, it clears at once,
because the mark's clear may have arrived while the role was down.

| Field | Type | Meaning |
|---|---|---|
| `at` | integer | The `at` of the mark, the Unix time of the press. |

    {"at": 1759140000}

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
terms as the reporter's. The operator does not read it: it reads the
role's recorded marks, and a mark that stops moving is the signal it
acts on. The topic is also on the bus for a client that folds the
store's marks and needs to know whether their writer is gone.

The jellyfin role reads it. Each `online` starts one reconcile, a read
of every Jellyfin user's played and resumable items published as
outside plays, because an outside play is not retained and a progress
role that was down lost every post Jellyfin made meanwhile.

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
