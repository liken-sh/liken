# 03, Read-only volumes

Built, and drilled in the lab on 2026-09-05 with development build
`0000.00.00-000-dev-008-bce97b16`. Two pods in two namespaces read the
forge's repository; a push reached both inside a 15 second `pull`; with
the forge stopped, an `allowStale` pod started and a `refuse` pod stayed
refused with `GitVolumeRefused` in its events; the forge's return
started the refused pod. The kubelet posted no volume health metric,
which the open problem on the volume condition now records.

## The problem

A pod wants a checkout of a repository that it never writes, and it
wants the checkout to follow a ref without a sidecar. Many pods in many
namespaces may want the same repository, on the same node, at the same
time.

## The design

### The volume

The volume is an inline ephemeral CSI volume with `readOnly: true`. The
kubelet marks it `csi.storage.k8s.io/ephemeral: "true"` in the volume
context and sends no stage call for it, so the whole lifecycle is
`NodePublishVolume` and `NodeUnpublishVolume`.

| Attribute | Values | Default |
|---|---|---|
| `url` | Any URL git accepts. Required. | none |
| `ref` | A branch or tag name. | `main` |
| `pull` | A Go duration, or `never`. | `5m` |
| `depth` | A positive integer, or `0` for a full clone. | `0` |
| `offline` | `refuse` or `allowStale`. | `refuse` |

The driver refuses, with `InvalidArgument` and a message naming the
attribute, an unknown attribute, a malformed value, an inline volume
without `readOnly: true`, and an inline volume with a
`nodePublishSecretRef` that is missing a usable credential. It also
refuses two URL shapes that would run a command: a URL that starts with
a dash, which git reads as an option, and git's `<transport>::<address>`
form, which names a helper program git runs. The driver fetches as root
on the node and the URL comes from a pod spec, so both are refused
before git sees them.

### The store

The store has one bare repository per URL at
`<store>/repos/<sha256 of url>/`, with the URL written beside it in
`url` so a person can read the directory. Every volume of that URL on
the node shares it. The repository fetches each followed ref into
`refs/git-csi/<ref>`, so one repository serves volumes on different
refs. A `depth` other than `0` applies to the first fetch of a
repository; a later volume with a different `depth` reuses what is
there.

A published tree is `<store>/volumes/<volume id>/tree`, a checkout of
the ref made with `git --work-tree`, bind-mounted read-only onto the
target path. The volume id is the CSI `volume_id`, which for an inline
volume is unique per pod.

### Following the ref

`pull` schedules a fetch per repository, at the shortest `pull` among
the volumes that share it. `never` on every volume stops the fetch.
When a fetch moves the ref, the driver checks the new commit out into a
directory beside the published tree and then replaces the published
tree entry by entry, one `rename` per file, recursing into directories
that exist on both sides. A reader sees the old file or the new one and
never a partial write.

The plan first said to swap the two directories with one `rename`. That
does not work: a bind mount follows the directory it was made from, not
the name, so every pod stays on the old tree after the swap. A fresh
bind over the target path reaches new pods only, and a symlink swap
shows the pod a symlink. Replacing entries inside the directory the pod
holds is the one way a running pod sees the new commit through a plain
directory.

The same placement serves a first publish, where the whole checkout
arrives with one `rename`, and a publish the kubelet repeats after the
driver restarted, where the tree a pod still reads is brought to the
commit without a remount.

### Offline

When the fetch at publish fails, `offline: refuse` fails the publish
with `Unavailable` and the git error, so the pod stays in
`ContainerCreating` with the reason in its events. `offline:
allowStale` publishes the ref as the store holds it, when it holds it,
and marks the volume's condition abnormal with the fetch error until a
fetch succeeds. A repository the store has never fetched is refused
under both.

### Credentials

`nodePublishSecretRef` on an inline volume names a `Secret` in the
pod's namespace, and the kubelet passes its data to the driver. The
driver accepts two keys: `ssh-privatekey`, written to a file with mode
`0600` under `<store>/volumes/<volume id>/` for the duration of the
call and passed with `GIT_SSH_COMMAND`, plus `known_hosts` when that
key is present; and `token`, passed as a credential helper that answers
`password=<token>` for an HTTPS URL, with `username` defaulting to
`git`. The file never outlives the call. `git daemon` in the lab needs
none of this, so the lab drills credentials only through the unit
tests, against a bare repository served over SSH on the host if one is
available, and skips them otherwise.

### Stats and condition

`NodeGetVolumeStats` reports the tree's total size in bytes as `used`
and `available` as `0`, and a `VolumeCondition`. The condition is
normal after a successful publish or fetch. It is abnormal, with a
message, after a failed fetch under `allowStale`, and after a fetch
that finds the ref deleted upstream.

### Events

The driver posts an `Event` on the pod, through `podInfoOnMount`, for a
refused publish, a stale publish, and a fetch that fails after
succeeding before. Posting an `Event` needs `create` on `events` in
every namespace, which `rbac.yaml` adds as a `ClusterRole` bound to the
`ServiceAccount`.

### What the program looks like

The plan leaves the file layout to the builder, with two constraints.
Every git invocation goes through one function that takes the
repository directory, the environment for credentials, the arguments,
and a context with a deadline, and returns stdout, stderr, and the
exit code. Every operation on a repository takes a per-repository lock,
so a fetch never races a publish of the same URL.

## Considered and set aside

- **A work tree per volume with its own `.git`.** It costs a clone per
  pod and gives nothing a shared bare repository does not.
- **Following a ref with `git pull` in the checkout.** A checkout that
  pods read while it changes is exactly what the shared bare repository
  and the atomic swap avoid.
- **A `PersistentVolume` for read-only volumes.** It binds to one claim
  in one namespace, and the read-only case is many pods in many
  namespaces. The inline form has no binding.

## Proof

- Unit tests run against real repositories the tests create with the
  git binary, served with `git daemon` on a random port, or read
  through a `file://` URL where the transport does not matter. The
  tests cover every attribute value in the table, the swap, `offline`
  both ways, and each refusal.
- In the lab, `lab/drills/03.sh`: two pods in two namespaces mount
  `hello` from the host forge; a push to the forge reaches both inside
  `pull`; the forge is stopped and a third pod with `allowStale` starts
  while a fourth with `refuse` stays in `ContainerCreating` with the
  reason in its events; the forge returns and the condition clears. The
  drill prints the `DaemonSet` pod's memory from `kubectl top` at the
  end.
