---
title: Volume attributes
weight: 10
---

# Volume attributes

`volumeAttributes` set the repository of a volume and the part of it
that the driver checks out. They go in the `csi` block of an inline
volume or of a `PersistentVolume`. An inline volume and a
`ReadOnlyMany` `PersistentVolume` are read-only, and they accept every
attribute below. A `ReadWriteOncePod` `PersistentVolume` is writeable,
and it accepts `url`, `ref`, and `paths`. [Access modes](#access-modes)
gives the reason. The [read-only guide](../../guides/read-only/) and
the [writeable guide](../../guides/writeable/) show how to use them.
The driver refuses a volume that has an unknown attribute or a
malformed value, and an inline volume that does not set
`readOnly: true`. The error message names the attribute.

| Attribute | Values | Default |
|---|---|---|
| `url` | The repository to clone, as a URL that git accepts: `https://`, `ssh://`, `git://`, `file://`, or `user@host:path`. Required. | none |
| `ref` | The branch or tag to check out. | `main` |
| `pull` | A Go duration such as `5m` or `30s`, `on-demand`, or `never`. | `5m` |
| `depth` | The number of commits to fetch, as a whole number, or `0` for the full history. It applies only to the first fetch of the repository on a node. | `0` |
| `offline` | What to do when the fetch fails at pod start. `refuse` refuses the volume. `allowStale` mounts the node's last copy of the ref. | `refuse` |
| `webhookSecret` | The name of a `Secret` in the claim's namespace. The controller verifies a push from the forge against the `secret` key of this `Secret` before it marks this volume for a pull. | none |
| `paths` | The directories and files of the repository to check out, separated by commas or one on each line. [The paths attribute](#the-paths-attribute) gives the rules. | the whole tree |

`pull` takes one of three values.

| Value | Meaning |
|---|---|
| `never` | The volume never pulls and ignores demands. It keeps the commit it staged. |
| `on-demand` | The volume has no timer. It pulls only when something demands a pull. An inline volume refuses this value, because a demand is an annotation on a `PersistentVolume`, and an inline volume has no `PersistentVolume`. |
| A duration such as `5m` | The volume pulls on that interval, and it also pulls when something demands a pull. |

The driver fetches as root on the node, with a URL that comes from a
pod spec, so it refuses two kinds of URL before it runs git: a URL that
starts with a dash, which git would read as an option, and a URL of the
form `<transport>::<address>`, which makes git run a helper program.

## The paths attribute

`paths` lists the directories and files of the repository that the
driver checks out. The volume's tree has only those entries, each at
the same place that it has in the repository. So with
`paths: films`, the mount contains `films/`, and not the contents of
`films/` at its root. To make one directory the root of a container's
mount, also set `subPath` on the pod's `volumeMount`. A volume with no
`paths` checks out the whole tree. An inline volume, a read-only
claim, and a writeable volume each accept `paths`.

A volume attribute is a string, because Kubernetes defines
`volumeAttributes` as a map of strings. So `paths` is one string that
lists the entries. Separate them with commas:

```yaml
volumeAttributes:
  url: https://code.example.com/data/catalog.git
  paths: films,series/index.yaml
```

Or put one entry on each line, with a YAML block scalar:

```yaml
volumeAttributes:
  url: https://code.example.com/data/catalog.git
  paths: |
    films
    series/index.yaml
```

The driver trims the spaces around each entry, drops the empty
entries, and removes a trailing `/`. Then it sorts the entries and
removes duplicates, so two volumes with the same entries in another
order check out the same tree. An entry cannot contain a comma, and it
cannot start or end with a space. git reads each entry as a path and
not as a pattern, so `pages/[id]` names the directory of that name.

The driver refuses the volume with `InvalidArgument`, before it runs
git, when:

- an entry starts with `/`, because each entry is relative to the root
  of the repository;
- an entry has an empty, `.`, or `..` component, such as `a//b` or
  `a/../b`;
- an entry has `.git` as a component;
- the value has no entry after the split, such as `" , "`, because an
  empty list would check out the whole tree.

An entry that is a symlink checks out only the link. A symlink inside
an entry that points outside the entries does not resolve, because its
target is not in the tree.

When the commit has no file or directory at an entry, the result
depends on the kind of volume. A read-only volume refuses its first
stage, and later keeps its current commit, as
[an entry that a commit lacks](../../guides/read-only/#an-entry-that-a-commit-lacks)
describes. A writeable volume checks out nothing for the entry and
stages as usual, as
[an entry that the repository lacks](../../guides/writeable/#an-entry-that-the-repository-lacks)
describes.

The `csi` block of a `PersistentVolume` cannot change, so a staged
volume keeps its `paths`. To check out other paths, create a new
`PersistentVolume`.

The CSI specification limits each map field to 4 KiB, and the volume
context is a map field. The volume context contains every attribute,
and also the name, namespace, UID, and service account of the pod,
which the kubelet adds. When the list of entries grows long, name a
parent directory in place of the directories inside it.

## Access modes

The access mode of a `PersistentVolume` decides whether the driver
stages a writeable volume or a read-only claim. The driver refuses
every other access mode, and its error message lists the modes that it
accepts. An inline volume has no access mode and is always read-only.

| Access mode | What the driver stages |
|---|---|
| `ReadWriteOncePod` | A writeable volume. See the [writeable guide](../../guides/writeable/). |
| `ReadOnlyMany` | A read-only claim, which the driver publishes to every pod on the node that uses it. |

A read-only claim accepts `pull`, `depth`, `offline`, and
`webhookSecret`. A writeable volume refuses all four, because it
updates from its ref only at stage. Both accept `url`, `ref`, and
`paths`. A pod must set `readOnly: true` on
the `persistentVolumeClaim` volume that uses a read-only claim, or the
driver refuses the publish.

## Credentials

An inline volume names its `Secret` in `nodePublishSecretRef`, and the
`Secret` is in the pod's namespace. A `PersistentVolume` names the same
`Secret` in both `nodeStageSecretRef` and `nodePublishSecretRef`. The
stage fetches with the first. The kubelet sends the second with every
publish call, and again on each pod sync, which is how a restarted
driver gets the credential back. The driver refuses a publish whose
`Secret` differs from the stage's. The driver reads these keys:

| Key | Use |
|---|---|
| `ssh-privatekey` | The private key for an SSH URL. |
| `known_hosts` | The host keys that ssh trusts, in the `known_hosts` format. With it, the host key must match. Without it, ssh accepts the first host key that it sees and refuses a later change. |
| `token` | The password for an HTTPS URL. |
| `username` | The user name to send with the token. Default `git`. |

The driver refuses a `Secret` that has neither `ssh-privatekey` nor
`token`.

## Events

| Reason | When |
|---|---|
| `GitVolumeRefused` | The driver refused to publish the volume. The message gives the reason. |
| `GitVolumeStale` | The fetch failed, and `offline: allowStale` made the driver mount the node's last copy. |
| `GitFetchFailed` | A fetch failed after one that worked. The driver posts it once, until a fetch succeeds. |
| `GitFetchRecovered` | A fetch succeeded after a `GitFetchFailed` or a `GitVolumeStale`. It is a `Normal` event, and it shows when the fault ended. |
| `GitVolumePathMissing` | The ref of a read-only volume moved to a commit that has no file or directory at an entry of `paths`. The tree stays on its commit, and the message names the entry and both commits. The driver posts it once, until the tree reaches a commit with every entry. It keeps this fault in memory, so a restarted driver posts it again at the next pull. |
| `GitVolumePathFound` | The tree reached a commit that has every entry of `paths` again, after a `GitVolumePathMissing`. It is a `Normal` event. |
| `GitVolumeNoPublishSecret` | The `PersistentVolume` has `nodeStageSecretRef` and no `nodePublishSecretRef`, so the driver loses the credential when it restarts. The driver posts it once for each stage and once for each restart. |

When the driver posts the same event with the same message on the same
object again, it increases the count of the existing event instead of
creating a new one. The kubelet retries a refused mount with a backoff
that grows to about two minutes, so a pod that is refused for an hour
shows one `GitVolumeRefused` line, such as `(x37 over 1h)`. The API
server deletes an event one hour after its last change.

## The gauge

The gauge `git_csi_volume_abnormal`, with the labels `namespace` and
`volume`, is 1 after a stale publish and after a failed fetch, until a
fetch succeeds. It is also 1 while the commit of the ref has no file
or directory at an entry of `paths`. The driver's log says what went
wrong when the gauge rises and when it falls. The CSI call
`NodeGetVolumeStats` reports the size of the files in the tree as the
space used. With `paths`, the tree contains only the entries.
