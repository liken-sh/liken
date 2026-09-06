# 14, Watch state and people

Every `Play` starts at the beginning. Nothing records that a movie was
half watched, that a season is on episode four, or who was in the room.
This plan adds a `Person` to the cluster, a `Watch` to the library, and
a store of progress that every screen holds a copy of.

## The problem

A household watches in subsets. Three people are on season three of one
series together, two of them are on episode nine of another, and one of
them is on season seven of the first series alone. Every service that
records progress keys it on one account, so a shared series is wrong
for everyone who shares it. The record has to belong to the set of
people who watched, and a person has to be a fact of the cluster before
a set of them can be.

## The design

### A `Person` is a cluster fact

A new repository, `people-operator`, ships one cluster-scoped CRD and no
controller: `Person` in `people.liken.sh/v1alpha1`.

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: thora
spec:
  displayName: Thora
  avatar: claim://portraits/thora.png
  child: true
```

The resource is small because its value is in who names it. A
`Person` is a subject the way a `ServiceAccount` is: a `Play` names the
people who watched it, a `Watch` names the people who share it, and a
later `MediaPreferences`, `Remote`, or `RoleBinding` can name one too.
Each of those is a plan in the operator that reads the `Person`. This
repository is the first.

Two fields are named now and filled by later plans. `spec.uid` is a
Linux uid, so files written for a person are owned by that person from
the first write. A person may state their own, for a NAS that already
holds their files under it. A later controller assigns one from a
reserved range when none is stated. `spec.identity` names an outside
login, an OIDC issuer and subject, and nothing reads it yet.

### A `Watch` is a set of people on one item

A `Watch` is namespaced, in the namespace of the `Library` that holds
the item.

```yaml
apiVersion: library.liken.sh/v1alpha1
kind: Watch
metadata:
  name: the-office-with-the-girls
  namespace: media
spec:
  people: [chris, thora, io]
  item:
    library: series
    slug: the-office
status:
  play: living-room-the-office-s03e04-x7k2q
  item: 1
  position: "0:21:40"
  duration: "0:22:05"
  season: 3
  episode: 4
  ended: true
  lastRecorded: "2026-09-06T21:14:02Z"
```

A person writes one by hand, or the browser creates one when it asks
who is watching. The status is a projection of the progress store, and
the operator rewrites it as rows arrive. It says which `Play` was
recorded last against the `Watch` and where that `Play` reached. What
comes next is a walk over the catalog's season structure, so the
browser answers that from its two local files, and the status does
not. Two `Watch`es on the same item with different people are two
records, and both are right: the three of them are on S03E04 and one
of them is on S07E12 alone.

A `Watch` carries an owner reference to each `Person` in it. When the
last of them is deleted, the garbage collector removes the `Watch`.

### A `Play` names its people through owner references

The browser's play request gains two fields: the names of the people
watching, and the item's aliases, which the browser reads from the
catalog beside it. The operator creates the `Play` as it does today, and
stamps both onto its metadata:

```yaml
metadata:
  ownerReferences:
    - apiVersion: library.liken.sh/v1alpha1
      kind: Watch
      name: the-office-with-the-girls
    - apiVersion: people.liken.sh/v1alpha1
      kind: Person
      name: chris
    - apiVersion: people.liken.sh/v1alpha1
      kind: Person
      name: thora
  annotations:
    library.liken.sh/alias.tmdb: "2316"
    library.liken.sh/alias.imdb: tt0386676
    library.liken.sh/alias.tvdb: "73244"
    library.liken.sh/season: "3"
    library.liken.sh/episode: "5"
```

Owner references rather than a field, because then the `Play` schema
does not change. A namespaced `Play` may name a cluster-scoped `Person`
and a same-namespace `Watch`, and the garbage collector deletes a
dependent only when none of its owners exist. Both rules come from the
Kubernetes owners-and-dependents page and the original garbage
collection design.

Annotations rather than labels, because a label value stops at 63
characters and an alias list has no such rule. The aliases are the
work's identity. A URI names a file, and a 4K upgrade or a rename by the
organizer is a new file and the same position. An episode has no id of
its own outside a series, so the season and episode numbers travel
beside the aliases.

A `Play` with no people is still a `Play`. A fresh cluster has no
`Person`, and the browser creates a `Play` with no owners and no
`Watch`. The progress store records it against the `Player` alone.

### The progress store is a second `Corrosion` cluster

Progress is a SQLite database replicated by `Corrosion`, in a cluster
of its own, separate from the catalog. The catalog is rebuilt by a
rescan and progress is not, so the two have different durability rules
and stay apart. Every screen's browser pod runs a second agent beside
its catalog agent, and keeps the file on the same local-path claim, so
the browser starts with progress already on disk the way it starts with
the catalog.

One standing pod per namespace, the progress pod, holds the durable
copy on a claim of its own. The operator stands it beside the catalog
pod, owned by the same `Catalog`, and it is the pod a rebuilt cluster
starts from. It has its own pod, claim, and cluster rather than a
place in the catalog pod, so nothing in it shares a lifecycle with the
scanners. The agent answers on its own ports, 8081 for the API and
8788 for gossip, so a screen can run it beside the catalog agent
later.

Three tables:

```
plays        play, player, library, watch, started, ended, item,
             position, duration, phase, season, episode, recorded
play_people  play, person
play_aliases play, provider, id
```

One row per `Play`. One row per person in it. One row per alias on it.
The audience of a `Watch` is a query over `play_people`, not a column.
"Continue watching for chris" is every audience that contains chris,
latest row per item. History is the same table read backward. The
tables key on aliases and on `Person` names, never on catalog ids, so
the store never reads the catalog. The browser joins progress to the
catalog at read time, alias to alias, across its two local files.

### The progress pod reads the bus and writes the rows

The progress pod holds no Kubernetes credential, like every pod this
operator stands. The operator is the only API client, so every fact
the store needs from the API crosses the bus, retained, and every mark
the operator needs from the store crosses it back. `progressbus.go`
names the topics and the payloads.

The playback pod's sidecar already publishes each `Play`'s report on
`liken/media/plays/<namespace>/<name>/status`: the item, the position,
the duration, and the paused flag, retained. The operator publishes
what the sidecar cannot know on `liken/library/plays/<namespace>/<name>/audience`:
the `Player`, the `Watch`, the people, and the aliases, read off the
`Play`'s owner references and annotations. The progress pod subscribes
to both for its namespace, writes the rows, and publishes what it
wrote last on the `Play`'s `recorded` topic.

The playback pod learns nothing of people. The browser writes nothing.
The three are joined only by the topics and the table.

The bus drops what nobody hears. If the progress pod restarts in the
middle of a film, the reports in that window are gone. Two things close
the gap. When a `Play`'s phase is `Finished` or `Failed`, the operator
publishes its `status.position` off the API on the `Play`'s `final`
topic, and the progress pod records it and marks the row ended. And
the operator sets a finalizer, `library.liken.sh/progress`, on every
`Play` in a namespace that holds a `Catalog`, at creation for the
`Play`s it creates and on the next pass for any other, and removes it
only when the `recorded` mark says the row ended. A `Play`'s
`ttlSecondsAfterFinished` delete cannot outrun the write. When the
`Play` is gone, the operator clears its three retained topics.

### Deleting a `Person` deletes their progress

The library operator puts a finalizer on every `Person`. On delete, it
publishes a forget request on `liken/library/people/<name>/forget`,
every namespace's progress pod removes that person's `play_people`
rows and answers on its own `forgotten` topic, and the operator removes
the finalizer once every namespace that holds a `Catalog` has
answered. A `plays` row with no people left
stays as a `Player`'s row. A shared `Watch` keeps the people who
remain. The garbage collector removes the `Play`s and `Watch`es that
named only that person.

### The home screen asks who is watching

When the browser wakes from the idle screen after a long enough sleep,
it shows the `Person` list and asks who is watching. The answer becomes
the people on every play request until the next sleep.

The home screen gains a continue-watching row from the store, and a
history page reads the same rows backward. `media-operator` changes
nothing in this plan.

## Failure

- **The broker is down.** No reports arrive. The progress pod reconciles
  from `Play` status when the broker returns, so the end position lands
  and the positions between are lost.
- **The progress pod is down.** Same gap, same heal. Screens read their
  local copy and see progress up to the last row that reached them.
- **A screen's claim is lost.** The browser's agent syncs the store from
  its peers on first start, as the catalog agent does.
- **The progress pod's claim is lost.** The durable copy is gone. The
  agents on every screen still hold full copies, and a new progress pod
  syncs from them. Only a cluster with no screen up loses the history.
- **Late or missing progress at 2 a.m.** The chain is the playback
  pod, the broker, the progress pod, `Corrosion`, and the screen's
  file. `Watch.status.lastRecorded` says how far the chain got.

## What is set aside

- **A separate operator for progress.** It would depend on nothing of
  the library's, and record every `Play` in the cluster. It loses
  because a `Watch` names a series, and only the catalog can say what a
  series is or what its next episode is. The split would leave the
  store in one repository and every reader in another. The loose
  coupling stays inside this repository, so the store can leave later.
- **`git-csi-driver` for the durable copy.** A git-backed volume is a
  fine backup and too slow for the sync between screens. `Corrosion` on
  the progress pod's claim is the durable copy.
- **dqlite.** Weighed for this shape and set aside; see
  [`rejected/dqlite.md`](rejected/dqlite.md).
- **A `Zone` resource.** A room is `Player.spec.zone`, a string.
  Presence, who is in which room now, is a later design and this plan
  does not wait on it.
- **The operator as an identity provider.** `spec.identity` links to
  one. The cluster does not need one to have people.

## What is not decided

- **The second agent's memory on a 1 GB screen.** Unmeasured. If it
  costs too much, the store and the catalog may have to share one agent
  and one file.
- **Retention.** How long a `plays` row stays, and whether history
  needs a cap.
- **Finished.** The position past which a movie counts as watched, and
  how the browser picks the next episode from a `Watch`'s status and
  the catalog.
- **The wake-up question.** How long a sleep before the browser asks
  again, and how the picker looks.
- **Default people per screen.** A bedroom screen should ask nobody,
  and a living-room screen should open with the people who usually sit
  there. Where that default lives, on the `Player` or elsewhere, is
  open, and this plan asks nothing of `media-operator` until it is
  decided.
- **The `Person` controller.** When uid assignment and a signed client
  certificate per person are worth building, and which operator builds
  them.
- **The progress cluster in `Catalog` status.** `Ready` and
  `status.members` follow the catalog pod and its agents alone. A
  progress pod that never starts is invisible in `kubectl get
  catalogs`.
- **The local harness.** `local/catalog` stands one catalog agent and
  one reporter, and no progress agent. A local round on the browser's
  continue-watching row needs one.
