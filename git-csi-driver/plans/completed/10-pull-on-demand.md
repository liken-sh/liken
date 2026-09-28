# 10, Pull on demand

Built on 2026-09-06 and drilled in the lab with development build
`2026.09.06-002-dev-003-50be2024`: drill 10 passed, with one pull and
one move from a burst of twenty demands, and drills 03 and 07 still
pass. One thing changed from the draft while building it. The report
with the last demand and the last pull reaches a person through the
driver's log alone, because `NodeGetVolumeStats` carries no message
in this CSI version. Plan 12 drilled the controller's half of the
restart rule.

## The problem

A read-only volume finds that its ref moved when its `pull` timer
fires. `pull` defaults to `5m`, so a person who pushes to the forge
waits up to five minutes for the change to reach the pods. Shortening
`pull` costs a fetch per volume per interval on every node, whether or
not anything was pushed.

The driver needs a way for something outside the node to say "pull
now". This plan builds that channel and nothing else. Plan 12 builds
the listener that turns a forge's webhook into a demand on this
channel, but the channel is complete without it: a person, a script,
or any tool that can annotate a `PersistentVolume` demands a pull the
same way.

This plan is about read-only volumes. Writeable volumes stay as they
are, and the section "What stays out" says why.

## The design

### The channel is an annotation on the `PersistentVolume`

A demand is one annotation on the `PersistentVolume`:

    git.liken.sh/pull-requested-at: "2026-09-06T14:31:07Z"

The `PersistentVolume` is the right object. It is the object that
carries `url` and `ref`, in `spec.csi.volumeAttributes`, so a demand
names the same object the node plugin staged from. It is
cluster-scoped, so nothing is written into a tenant's namespace. Its
`spec.csi` block is immutable after creation, and its metadata is not,
so an annotation is a legal write.

The node plugin acts when the value differs from the one it last acted
on. Any new value works. The timestamp is a convention, and it is what
a person reads in `kubectl describe` to see when the last demand came.

### The node plugin holds one watch

The node plugin holds one watch on `PersistentVolume`s for the whole
node. On each event it reads `spec.csi.volumeHandle`, finds the staged
volume with that handle, and compares `git.liken.sh/pull-requested-at`
to the value it last acted on for that volume. When the value differs,
it wakes the volume's follower to pull at once.

The watch restarts when it closes, and it reads the list again on a
resync, the way `arming.pass` does today for a claim. Its context
descends from the driver's run, so the pod's stop ends it.

One watch per node covers every volume the node stages, rather than
one watch per volume. The node plugin already holds `get`, `list`, and
`watch` on `persistentvolumes` in `deploy/rbac.yaml`, so this plan
adds no RBAC rule.

### The pull goes through the follower

`follow.go` holds one `follower` per repository per node, shared by
every volume of that URL. `follower.nudge` resets the interval timer
and does not fetch. This plan adds a second wake that runs
`follower.tick` at once. `tick` walks every volume of the repository,
so one demand moves every volume of that URL on the node.

A demand pulls at once unless the follower pulled less than
`--demand-min-interval` ago. In that case the follower schedules one
pull at the end of that interval and drops every further demand until
that pull runs. The default is `10s`, which bounds a burst to six pulls
a minute per repository per node. The drill measures it.

A pull a demand started reports exactly as a pull the timer started,
because it is the same code. `follower.trouble` posts one
`GitFetchFailed` on the first failure after a success, and the volume's
report carries the failure until a pull works. This plan adds no
failure path of its own.

### `pull` has three meanings

The `pull` attribute gains one word. The reference table in
`docs/content/docs/reference/attributes.md` reads:

| Value | Meaning |
|---|---|
| `never` | No timer and no demand. The volume holds the commit it staged for its whole life. |
| `on-demand` | No timer. The volume pulls only when something demands it. |
| A duration such as `5m` | The volume pulls at least that often, and it pulls when something demands it. |

The default stays `5m`.

`never` keeps its one meaning. A volume with `never` joins no follower,
and a demand does not reach it. `on-demand` joins the follower with no
timer, so the follower's interval is the shortest duration among the
volumes that share the repository, or none when every volume is
`on-demand`.

### An inline volume refuses `on-demand`

An inline volume is written into the pod spec under `volumes[].csi`
and has no `PersistentVolume`. Nothing can annotate it, so nothing can
demand it. A stage that finds `pull: on-demand` on an inline volume
refuses with an error that says so, the way a writeable stage refuses
`pull` in `parseStageAttributes`. An inline volume with a duration
still moves with a demand when a claim on the same node names the same
URL, because the follower is per repository, and that is a side effect
a person does not rely on.

### A restart pulls

A demand that arrives while the node plugin restarts is lost, and with
`pull: on-demand` no timer covers the loss. So a restart is a demand.
When the node plugin takes its volumes back from the store in
`resume`, every read-only volume with `on-demand` or a duration pulls
once. The controller's own restart is plan 12's half of the same rule.

### What the driver reports

**The volume's report** carries two times beside what it carries
today: when the last demand came, and when the last pull ran. The
report is the line `noteHealth` writes to the driver's log when the
volume's health turns, so a person reads the demand, the pull, and
the failure on one line.

**The node's metrics** take one counter,
`git_csi_demanded_pulls_total`, labeled `namespace` and `volume`, the
labels `git_csi_volume_abnormal` already takes.

**No new `Event`.** A pull that moves the tree posts no `Event` today,
in `follower.refresh`, and a pull a demand started posts none either.
A repository pushed twenty times an hour would put twenty `Event`s on
the claim. An `Event` is for a state change a person has to see.

### The manual demand

The channel is an annotation, so a person demands a pull with one
command:

```console
kubectl annotate pv franchises git.liken.sh/pull-requested-at="$(date -u +%FT%TZ)" --overwrite
```

This is the whole answer for a forge that plan 12 does not verify, and
it is what a person runs to test the channel before any forge is
configured.

### What stays out

**Writeable volumes.** Invariant 2 of the design is that while a pod
holds the tree, only the application changes it, and a rejected push.
Upstream reaches a writeable tree at stage, before the pod starts, and
through the rebase plan 09 describes. A demand that pulled into a
mounted writeable tree would rewrite the pod's files at a moment
nobody chose. A demand on a writeable volume's `PersistentVolume` does
nothing, and the log says so once.

## Considered and set aside

- **A `ConfigMap` per repository, in the driver's namespace, that
  every node plugin watches.** It reaches inline volumes and claims
  alike. It loses because the driver would create and delete objects
  of its own, and would need a sweep for the ones no volume follows.
  The annotation needs no object that does not already exist, and an
  inline volume is not long-lived enough to want a demand.
- **`pull: 0` for `on-demand`.** Shorter, but a zero interval reads as
  a mistake in a manifest, and `0` already means "the default" inside
  `parsePull`.

## The drill

Drill 10, `lab/drills/10.sh`.

1. Apply a `ReadOnlyMany` `PersistentVolume` and claim on the forge's
   repository with `pull: on-demand`, and a reader pod. The pod reads
   the greeting.
2. Push a new greeting to the forge. Wait 60 seconds. The pod still
   reads the old greeting, because nothing demanded a pull.
3. Annotate the `PersistentVolume`. The pod reads the new greeting
   within 30 seconds.
4. Push again, then annotate twenty times inside five seconds. The
   driver's log shows one pull, or two, inside
   `--demand-min-interval`.
5. Push again, then delete the driver pod on the node. The new pod
   takes the volume back, and the pod reads the third greeting without
   an annotation.
6. Apply a second claim with `pull: never` on the same repository.
   Annotate its `PersistentVolume`. Its pod never reads the new
   greeting.
7. Run an inline volume with `pull: on-demand`. The stage is refused,
   and the pod's `Event` says why.

Drill 03 and drill 07 still pass, because the timer path is unchanged.

## What is done when

- The node plugin holds one watch on `PersistentVolume`s, wakes the
  follower of a volume whose `git.liken.sh/pull-requested-at` changed,
  and pulls at once, bounded by `--demand-min-interval`.
- `pull` accepts `on-demand`, with the three meanings in the table
  above, and an inline volume refuses it.
- A node plugin restart pulls every read-only volume that is not
  `never`.
- A demand on a writeable volume does nothing.
- The volume's report carries the last demand and the last pull, and
  `git_csi_demanded_pulls_total` counts the pulls.
- The unit tests prove the watch that reconnects, the wake, the
  coalescing, the three meanings of `pull`, the inline refusal, and
  the restart pull. Coverage stays at 100%.
- Drill 10 passes in the lab, and drills 03 and 07 still pass.
- The read-only guide has a section on demanding a pull, with the
  three meanings of `pull` and the manual command. The reference table
  has the `on-demand` row. The install guide lists
  `--demand-min-interval`.
