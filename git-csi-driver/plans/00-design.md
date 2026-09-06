# Design

`git-csi-driver` mounts a git repository as a Kubernetes volume. A
read-only volume is a checkout that follows a ref. A writeable volume
is a checkout the driver commits and pushes on the application's
behalf. The application sees a plain directory and never sees `.git`.

The driver name is `git.liken.sh`. It runs on a `liken` cluster, and on
any other cluster whose CSI plumbing is standard.

## The two uses

**Data repositories, read by many.** A repository of curated data, such
as a tree of YAML files, is mounted into any pod in any namespace as an
inline volume. The driver keeps the checkout current with the ref.

**Application configuration, written by one.** Many self-hosted
applications keep their configuration as text files in one directory,
and edit those files through their own user interface. That directory
becomes a repository with history, an off-machine copy, and a restore
path: delete the claim, make a new one against the same repository, and
the application starts from its last push.

## What the driver is not

- It is not a provisioner. It never creates a repository or a branch.
  A person makes the repository on a forge, and the driver clones it.
- It is not a merge tool. It fast-forwards and rebases, and it stops
  at the first conflict for a person to resolve.
- It is not a database. A volume holds text a person would also want
  to read as history. Databases, caches, and media do not belong on
  it, and the size guard exists to keep them off.
- It is not all of git. A checkout is one ref of one repository. A
  submodule's directory is empty, a Git LFS pointer is checked out as
  the pointer, and a writeable volume takes no `depth`. Each becomes a
  plan when a use asks for it.

## The invariants

1. **The driver is the only committer.** The repository lives in the
   driver's store on the node. The pod gets a bind mount of the work
   tree, with no `.git` inside it.
2. **While a pod holds the tree, only the application changes it.**
   Upstream changes reach the tree at stage, before the pod starts,
   and never later. An application reads its configuration at start,
   so this is the one moment a pull is safe.
3. **The driver merges nothing and loses nothing.** At stage it
   fast-forwards, or it rebases local commits onto upstream. A rebase
   that conflicts is aborted, and the volume moves to a side branch.
4. **Durability is the last push.** Commits are local and frequent.
   Pushes follow the policy, and every unpublish pushes without
   condition.
5. **A writeable volume is `ReadWriteOncePod`.** The driver refuses to
   publish a volume it may push under any other access mode. One pod,
   one node, one writer.
6. **A writeable volume is unarmed until its claim names a policy.**
   An unarmed volume publishes, watches, and reports what it would
   commit. It commits nothing and pushes nothing. This is the consent
   step, and it is where a person writes the ignore list before the
   first commit can carry a secret.

## The objects

The design uses only the objects CSI already has. It defines no custom
resource.

### The `PersistentVolume` is identity

The `csi` block of a `PersistentVolume` is immutable after creation, so
it holds only what defines the volume: the repository, the ref, and the
credentials.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: homeassistant-config
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOncePod]
  persistentVolumeReclaimPolicy: Retain
  csi:
    driver: git.liken.sh
    volumeHandle: homeassistant-config
    volumeAttributes:
      url: git@code.example.com:home/homeassistant.git
      ref: main
    nodePublishSecretRef:
      name: homeassistant-deploy-key
      namespace: home
```

`capacity` is required by the API and means nothing to git. The
driver reports the real size of the tree through `NodeGetVolumeStats`.

### The `VolumeAttributesClass` is policy

A `VolumeAttributesClass` is the cluster owner's word for how a
writeable volume commits and pushes. The claim names one, and that
field on the claim is mutable, so a policy change never restarts the
application. The `external-resizer` sidecar carries the change to the
driver through `ControllerModifyVolume`.

```yaml
apiVersion: storage.k8s.io/v1
kind: VolumeAttributesClass
metadata:
  name: config-eager
driverName: git.liken.sh
parameters:
  push.quiesce: 30s
  push.maxLatency: 5m
  commit.maxFileSize: 1Mi
  commit.author: Home Assistant <homeassistant@home.example>
  ignore: ".storage/,*.db*,*.log"
```

| Parameter | Meaning |
|---|---|
| `push.quiesce` | Push after this long with no writes. |
| `push.maxLatency` | Push anyway when the oldest unpushed commit is this old. |
| `commit.maxFileSize` | A larger file is not committed. It is named in the events and the log. |
| `commit.author` | The author and committer of every commit. |
| `ignore` | Patterns the driver adds to the repository's own `.gitignore`. |
| `metadata` | `true` to record modes, owners, and empty directories. The default. |

### The `PersistentVolumeClaim` binds and chooses

The claim names the volume and the class. Setting the class arms the
volume. The binder pairs a claim and a static volume only when both
name the same class, so a claim that names a class before it binds
needs the same `volumeAttributesClassName` on the `PersistentVolume`.
A bound claim takes a class change on its own.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: config
  namespace: home
spec:
  volumeName: homeassistant-config
  volumeAttributesClassName: config-eager
  accessModes: [ReadWriteOncePod]
  resources: {requests: {storage: 1Gi}}
```

### An inline volume is the read-only form

A read-only volume needs no `PersistentVolume` and no claim. The pod
spec is the mutable home, so read-only policy lives in the attributes.
The claim form at the end of this section takes the same attributes.

```yaml
volumes:
  - name: franchises
    csi:
      driver: git.liken.sh
      readOnly: true
      volumeAttributes:
        url: https://example.com/data/franchises.git
        ref: main
        pull: 5m
        depth: "1"
        offline: allowStale
```

| Attribute | Meaning |
|---|---|
| `url`, `ref` | The repository and the branch or tag to follow. |
| `pull` | How often the driver fetches. `never` follows nothing after stage. |
| `depth` | Clone depth. The default is a full clone. |
| `offline` | `allowStale` publishes from the node's cache when the remote is unreachable. The default refuses. |

A workload that names its storage as a claim cannot mount an inline
volume. For that workload, the shared form of a read-only volume is a
static `PersistentVolume` with the access mode `ReadOnlyMany` and the
same attributes. One tree per volume handle on the node serves every pod
that publishes it, and the claim takes the volume's `Event`s beside the
pods.

### What git carries

The repository carries two things for the driver and no configuration.

- **`.gitignore`** is the repository's own, and the driver honors it
  beside the class's `ignore` list.
- **Modes, owners, and empty directories** are recorded on a
  driver-owned ref, `refs/git-csi/metadata`, beside the branch. Git
  stores none of them, and a restore without them gives an
  application files with the wrong permissions and none of the empty
  directories it expects. The ref never appears in the checkout or in
  the forge's file view.

## The components

**The node plugin** is a `DaemonSet`. It stages, publishes, watches,
commits, and pushes. Its store holds one bare repository per URL and
one work tree per writeable volume. On `liken` the store is on the
pod-storage partition, beside every other volume on the node. The plugin watches the claims bound to its
volumes to learn their current class.
Once an hour it sweeps the store: work trees nothing stages, bare
repositories nothing names, the refs under `refs/git-csi/` that no
volume follows, and a `git gc` in each repository that stays, so a node
that serves one repository for a year stays bounded.

**The controller plugin** is one small `Deployment`. It implements
`ControllerModifyVolume` and nothing else. It validates a class and
refuses a bad parameter, and the `external-resizer` sidecar records the
result on the claim. There is no `CreateVolume` and no
`DeleteVolume`.

**The node plugin** declares `STAGE_UNSTAGE_VOLUME`, `GET_VOLUME_STATS`,
and `SINGLE_NODE_MULTI_WRITER`. The last one makes the kubelet send the
access mode `ReadWriteOncePod` uses; without it the kubelet sends the
legacy single-node mode to every driver.

**The `CSIDriver` object** declares `Persistent` and `Ephemeral`
lifecycle modes, `podInfoOnMount: true` so the node plugin knows the
pod and can post events on it, and `fsGroupPolicy: None` so the driver
owns file modes.

## The lifecycle of a writeable volume

1. **Stage.** Fetch the ref. Reuse the work tree from the last stage
   on this node, or add one. Reconcile: fast-forward if local is
   behind, rebase if local and upstream diverged, side branch if the
   rebase conflicts. Replay metadata. Mark the volume unarmed if the
   claim names no class.
2. **Publish.** Refuse any access mode other than `ReadWriteOncePod`.
   Bind-mount the tree onto the target path. Start the inotify watch,
   with a periodic `git status` sweep as the backstop.
3. **Run.** A write starts the quiesce timer. When it fires on an
   armed volume the driver records metadata, stages every change under
   the size guard, and commits. It pushes when `push.quiesce` or
   `push.maxLatency` says so.
4. **Unpublish.** Unmount. Commit what is pending, then push now.
5. **Unstage.** Push again if the last push failed. Keep the work tree
   for the next stage on this node.

A read-only volume stages and publishes, then fetches on `pull`. A
fetch that moves the ref fast-forwards the tree in place, because no
one writes there.

### Divergence

A push rejected as non-fast-forward, or a rebase aborted at stage,
moves the volume to `refs/heads/<ref>.<volumeHandle>`. The driver keeps
committing and pushing there, so no work stops and no work is lost.
The events and the log name both branches. A
person merges on the forge. At the next stage local is behind the
merged upstream, the volume fast-forwards back onto `<ref>`, and the
driver deletes the side branch.

### Restore

Delete the claim. Make a `PersistentVolume` against the same URL and a
claim on it. The pod starts on any node from the last push, with modes
and empty directories replayed from the metadata ref.

## Observability

Pure CSI has no status object, so the driver reports through the two
channels Kubernetes gives it. The CSI `VolumeCondition` was a third,
and `plans/rejected/the-volume-condition.md` says why it is not used.

- **Events.** Every state change posts an `Event` on the pod and on
  the claim: armed, unarmed, pending, pushed, push failed, file
  skipped, diverged, healed, swept, and for a read-only volume refused,
  stale, and fetch failed.
- **Metrics.** The node plugin exports per-volume gauges labeled by
  namespace and claim: `git_csi_armed`, `git_csi_pending_paths`,
  `git_csi_unpushed_commits`, `git_csi_last_push_timestamp_seconds`,
  `git_csi_push_failures_total`, `git_csi_skipped_files`, and
  `git_csi_diverged`. One more, `git_csi_volume_abnormal`, labeled by
  namespace and volume, is one while anything is wrong with a volume:
  a failed fetch or push, a stale publish, a skipped file, an unpushed
  commit older than `push.maxLatency`, an unarmed volume with pending
  paths, a diverged volume, or a ref the remote no longer holds.

Every fault reaches both, and the driver's log says what changed and
why. A cluster with alerting turns the gauge into a page. A cluster
without it reads the events.

## What the design accepts

- **The window between the last push and a dead node disk is lost.**
  Policy shrinks the window. Nothing closes it.
- **A short quiesce can commit a torn write.** An application that
  writes three files as one change can be committed between the
  second and the third. The class needs a floor, and the floor is a
  guess about the application.
- **A secret inside a configuration value is pushed.** The ignore list
  and the size guard catch files, not values. A private repository on
  a private forge is the mitigation.
- **The node plugin never learns that a `PersistentVolume` was
  deleted.** An age-based sweep removes work trees that are fully
  pushed and have not been staged for a long time.
- **An upstream rewrite older than the sweep age can take an object a
  work tree needs.** A work tree reads the repository's objects through
  alternates, and `git gc` at the sweep prunes what no ref has named
  for `--sweep-after`. An object stays reachable from the followed
  ref's history until upstream rewrites that history, and a rewrite
  older than the sweep age is the one case the store does not protect.
- **A forge inside the cluster is a loop.** A cluster that hosts its
  own forge cannot restore a volume onto a fresh node while the forge
  is down. `offline: allowStale` and the node's cache cover a restart.
  A forge outside the cluster covers a restore.

## `liken` integration

- The `DaemonSet` takes the node-critical priority class, so the
  unpublish push runs before the network goes down and before `liken`
  remounts disks read-only at shutdown.
- The store is a tenant of the pod-storage partition and shares its
  budget with every other volume on the node.
- Every failure the driver reports reaches the pod's events, the
  driver's log, and its metrics, which is the console-parity rule
  applied to a driver with no status object.
- The driver builds on CSI spec 1.13. Kubernetes 1.36 vendors spec
  1.9, and the two agree on the wire; the driver declares no capability
  the kubelet does not read.

## What exists elsewhere

The pieces exist as separate projects, and none of them combine.
Kubernetes had an in-tree `gitRepo` volume that cloned once at pod
start; it is deprecated. `git-sync` is a sidecar that pulls into a
shared `emptyDir` and never pushes. Flux's `GitRepository` fetches into
an artifact, not a volume. Volume populators fill a claim once and
stop. `gitfs` is a FUSE filesystem that commits and pushes, with no
Kubernetes story. `gitwatch` is a shell script that watches a
directory and commits.

## The plans

`plans/README.md` indexes the plans that build this design. Each plan
starts at low fidelity and is raised to full fidelity before anyone
builds it. A finished plan moves to `plans/completed/`, a dropped one
to `plans/rejected/`, and a problem the current work cannot solve is
written to `plans/open-problems/`.
