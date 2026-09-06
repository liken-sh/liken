# 05, Armed volumes

Proposed. Full fidelity.

## The problem

A person sets a `VolumeAttributesClass` on the claim, and from then on
the driver commits what the application writes and pushes it under the
class's policy. A restore from the last push has to give the
application its files with the right modes and its empty directories.

## The design

### The class

A `VolumeAttributesClass` with `driverName: git.liken.sh` carries the
policy in `parameters`. Every parameter has a default, so an empty
class arms a volume with the defaults.

| Parameter | Values | Default |
|---|---|---|
| `push.quiesce` | A Go duration, at least `5s`. | `30s` |
| `push.maxLatency` | A Go duration, at least `push.quiesce`, or `never`. | `5m`, or `push.quiesce` when that is longer |
| `commit.maxFileSize` | A Kubernetes quantity, or `0` for no limit. | `1Mi` |
| `commit.author` | `Name <email>`. | `git-csi-driver <git-csi-driver@liken.sh>` |
| `ignore` | Comma-separated `.gitignore` patterns. | empty |
| `metadata` | `true` or `false`. | `true` |

### The controller plugin

The controller plugin is the same binary with `--controller`, in a
`Deployment` `git-csi-driver-controller` with one replica and the
`external-resizer` sidecar at its newest release, with
`--feature-gates=VolumeAttributesClass=true`. It serves the `Identity`
service with `CONTROLLER_SERVICE` declared, and the `Controller`
service with `MODIFY_VOLUME` as its only capability.

`ControllerModifyVolume` validates the parameters against the table and
returns `InvalidArgument` naming the first bad parameter, or success.
It changes nothing itself: the node plugin reads the class the claim
ends up with. Every other `Controller` RPC returns `Unimplemented`.

The `external-resizer` needs the RBAC its release notes list, in a
`ClusterRole` bound to a `ServiceAccount` `git-csi-driver-controller`.

### The node plugin

Plan 04 left the node plugin watching claims and recording the class
name. This plan reads the class, resolves the policy with the table's
defaults, and applies it to the running watch without a remount. An
invalid class the resizer let through is treated as unarmed, with the
error in the condition.

### The commit

When the quiesce timer fires on an armed volume:

1. If `metadata` is true, the driver walks the tree and writes one
   record per path with a mode other than the default, an owner or group
   other than the process's own, or no entries. The record is a text
   file, one line per path, and it is committed to
   `refs/git-csi/metadata` as a single-file tree, only when its content
   changed since the last commit there.
2. The driver stages every path `git status --porcelain -z` reports,
   honoring the repository's `.gitignore`, the class's `ignore` list
   written to the git directory's `info/exclude`, and the size guard: a
   path over `commit.maxFileSize` is left unstaged and recorded in the
   skipped set.
3. If anything is staged, the driver commits with `commit.author` as
   author and committer and a message of the form `Update N paths`
   followed by the paths, one per line.

### The push

The driver pushes the ref, and the metadata ref beside it, when the
oldest unpushed commit is older than `push.quiesce` since the last
write, or older than `push.maxLatency`, and always at unpublish and
unstage. A push that fails is retried at the next timer, and the failure
is the condition's message until a push succeeds. A push rejected as
non-fast-forward is plan 06; in this plan it is reported like any other
failure.

### Restore

Stage on a node that has never held the volume clones, checks the ref
out, and, when `refs/git-csi/metadata` exists upstream, replays every
record: `chmod`, `chown`, and `mkdir` for the empty directories.

### Reporting

- The condition is abnormal for a failed push, for a skipped file
  (`N files over commit.maxFileSize: <paths>`), for an unpushed commit
  older than `push.maxLatency`, and for an invalid class.
- Events on the pod and the claim: `GitVolumePushed` with the commit
  and the count, `GitVolumePushFailed` with the error,
  `GitVolumeFileSkipped` with the path.
- Metrics from the design: `git_csi_unpushed_commits`,
  `git_csi_last_push_timestamp_seconds`, `git_csi_push_failures_total`,
  `git_csi_skipped_files`.

## Considered and set aside

- **Policy in a file in the repository.** It puts the driver's
  configuration in the application's tree, and the person who owns the
  tree did not ask for it.
- **Metadata in the tree.** The same objection. A driver-owned ref
  never appears in the checkout or on the forge's file view.
- **One commit per write.** An application that writes ten files in a
  second would make ten commits, and history would say nothing.

## Proof

- Unit tests run against real repositories, with a bare repository as
  the remote, and cover every parameter's defaults and refusals, the
  size guard, the ignore list, the metadata record and its replay, and
  the push timing.
- In the lab, `lab/drills/05.sh`: a pod writes, a class is set, the
  commit and push land on the host forge inside `push.quiesce`; a
  changed class takes effect with no restart; a file over the size
  guard is skipped and named in the condition; a pod deleted mid-timer
  pushes at unpublish; the claim and volume are deleted, recreated, and
  a new pod finds the files with their modes and the empty directory.
