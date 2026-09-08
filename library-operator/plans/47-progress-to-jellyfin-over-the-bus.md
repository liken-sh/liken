# Progress to Jellyfin over the bus

Plan 47. A small role on the media bus keeps a `Person`'s playback
progress the same in the store and in Jellyfin, in both directions.
A film started on a phone resumes on the television, and a film
started on the television resumes on the phone.

## The problem

The progress store (plan 14) holds where each person is in each
work, and the screens read and write it. A house that also runs
Jellyfin has a second copy of that fact, kept by Jellyfin's clients,
and the two never meet.

## The facts this rests on

These were read from Jellyfin's stable OpenAPI document, version
12.0.0, and from the Jellyfin and Webhook plugin sources on master
on 2026-09-08.

* The Webhook plugin posts on `PlaybackStart`, `PlaybackProgress`,
  and `PlaybackStop`. Its Generic destination posts any JSON body a
  Handlebars template renders, to any URL. The payload can carry the
  user name (`NotificationUsername`), the user id, the item id, the
  item type, the item's provider ids (`Provider_tmdb`, `Provider_imdb`,
  `Provider_tvdb`), `PlaybackPositionTicks`, `RunTimeTicks`,
  `IsPaused`, `PlayedToCompletion` on a stop, `SeriesId`,
  `SeasonNumber`, `EpisodeNumber`, `DeviceName`, and `ClientName`. An
  episode's provider ids are the episode's own, and the series' ids
  are not in the payload.
* The item listing, `GET /Items`, has no lookup by provider id. It
  can return every movie and episode with `ProviderIds`, `Path`,
  `SeriesId`, `ParentIndexNumber`, and `IndexNumber` in one recursive
  call with `fields=ProviderIds,Path`.
* One server API key acts as an administrator. The user-data write,
  `POST /UserItems/{itemId}/UserData?userId=<id>`, takes
  `UpdateUserItemDataDto` with `PlaybackPositionTicks`, `Played`, and
  `LastPlayedDate`, and an administrator may name any user.
  `GET /Users` lists the users with their ids.
* A tick is 100 nanoseconds. Ten million ticks are one second.
* The house's Jellyfin users are the Authentik users, and their user
  names are the `Person` names. No map is needed today. A field on
  the `Person` waits for the first name that differs.

## The design

### The Catalog names the Jellyfin server

```yaml
apiVersion: library.liken.sh/v1alpha1
kind: Catalog
spec:
  jellyfin:
    url: http://jellyfin.jellyfin.svc:8096
    secretRef:
      name: jellyfin-api-key
      key: token
```

`spec.jellyfin` is optional. When it is set, the operator stands one
pod per namespace beside the progress pods, `<catalog>-jellyfin`,
owned by the `Catalog`, running `/library-operator jellyfin`, with the
API key through a `secretKeyRef`, the way the enricher takes a
provider key. The operator also stands a `Service` of the same name on
port 8080, which is where Jellyfin's webhook posts. The pod holds no
Kubernetes credential, like every pod this operator stands.

The role's environment: the namespace, the bus address, the two topic
bases, `LIBRARY_JELLYFIN_URL`, `LIBRARY_JELLYFIN_API_KEY`, and
`LIBRARY_JELLYFIN_LISTEN`, which defaults to `:8080`.

### Inbound: a webhook becomes an outside play on the bus

The role listens for `POST /webhook`. The body is the JSON the plugin's
template renders; the manual states the template. The role turns one
post into one message on a new topic:

```
liken/library/plays/<namespace>/<play>/outside
```

The play name is `jellyfin-<user id>-<item id>`, so one person's
progress in one item is one row that moves, and a rewatch moves it
again. The payload, `outsidePlay` in `progressbus.go`:

```json
{
  "player": "jellyfin",
  "people": ["chris"],
  "aliases": {"tmdb": "603", "imdb": "tt0133093"},
  "season": 0,
  "episode": 0,
  "position": 4210,
  "duration": 8160,
  "ended": false,
  "at": 1757300000
}
```

`position` and `duration` are seconds. `at` is the Unix time of the
event, from the payload's `UtcTimestamp`. `ended` is true on a
`PlaybackStop`. `people` is the user name as a `Person` name.

For an episode the aliases are the series' provider ids, because that
is the identity the catalog gives an episode play, with the season
and episode numbers beside them. The role reads the series' ids once
per `SeriesId` through `GET /Items/{id}?fields=ProviderIds` and holds
them.

The message is not retained. A progress pod that is down for one
post catches the next, ten seconds later, and a stop repeats the
final position.

### The progress pod records an outside play

The progress role subscribes to its namespace's `outside` topics. One
message writes the row's audience and position in one apply, with
`recorded = at`, and marks the row ended when `ended` is true. A
message whose `at` is not newer than the row's `recorded` writes
nothing. That is the whole tie rule: the newer timestamp wins.

The store gets no new column. A screen's resume already reads the
latest play of a work by `recorded`, so a Jellyfin position that is
newer than the television's shows on the television with no change
to the browser.

### Outbound: a Play's position becomes a user-data write

The role subscribes to the same two topics per Play the progress pod
reads, the sidecar's `status` on the media tree and the operator's
`audience` on the library tree, for its namespace. It joins them in
memory per Play. Every ten seconds while the position moved, and on
the Play's `final`, it writes each person's position to Jellyfin:

```
POST /UserItems/{item id}/UserData?userId=<user id>
{"PlaybackPositionTicks": <position * 10000000>, "Played": <finished>, "LastPlayedDate": <at>}
```

`Played` is true on a final whose position is within the last two
minutes of the duration, or whose phase is `Finished` and the
position is at the end. A Play with no people writes nothing.

The role resolves the user name to the user id from `GET /Users`,
read at start and again on a miss. It resolves the work to the item
id from an index it builds from one recursive listing of movies and
episodes: movies keyed by each provider id, episodes keyed by the
series' provider ids with the season and episode numbers. It reads
the listing at start, again on a miss at most once a minute, and
every hour. The index is the role's only state, and it is rebuilt
from nothing on every start.

### The echo

The role remembers the last position it wrote per user and item. An
inbound post that carries that same user, item, and position within
one second is dropped. Whether the Webhook plugin fires on a
user-data write was not checked; the drop makes the answer moot.

### What the operator changes

* `Catalog.spec.jellyfin` in the CRD, `api.go`, and the manual.
* The pod and the `Service`, stood and re-stood the way the progress
  pods are, and removed when `spec.jellyfin` is removed.
* Nothing on the `Person`.

## The proof

* Unit tests for the webhook parse, the tick math, the episode
  series lookup, the index, the user map, the throttle, the echo
  drop, and the tie rule, against an `httptest` Jellyfin that
  answers the four endpoints from fixtures.
* The progress pod's tests for the `outside` record and the tie
  rule.
* A drill on the house: play a film on a phone, watch the
  television's continue row move; play on the television, watch
  Jellyfin's resume move.

## Open

* A `Person` whose Jellyfin user name differs. A field on the
  `Person` spec, read by the role, when the first one appears.
* A device name on the row. The store's `player` column names a
  `Player`; the row says `jellyfin` and nothing about which phone.
