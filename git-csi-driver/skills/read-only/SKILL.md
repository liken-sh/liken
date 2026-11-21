---
name: read-only
description: "Mount a git repository read-only, inline in a pod spec or through a claim, with pull on demand, webhooks, and paths to check out only some directories. Use when a pod needs a checkout it never writes, including a checkout of a private repository or of a few directories of a large repository."
---

This skill is the guide at https://liken.sh/git/docs/guides/read-only/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Mount a repository read-only

A read-only volume mounts one ref of a git repository as a directory
that the pod can read but not write. The simplest form is an inline CSI
volume in the pod spec. It needs no `PersistentVolume` and no claim, so
any pod in any namespace can mount any repository that the node can
reach. A workload that takes its storage from a `PersistentVolumeClaim`
uses the claim form, below.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: reader
spec:
  containers:
    - name: reader
      image: debian:sid-slim
      command: ["sleep", "infinity"]
      volumeMounts:
        - name: data
          mountPath: /data
          readOnly: true
  volumes:
    - name: data
      csi:
        driver: git.liken.sh
        readOnly: true
        volumeAttributes:
          url: https://example.com/data/franchises.git
          ref: main
          pull: 5m
```

The mount contains the files of the ref and no `.git` directory. All
volumes on a node that use the same URL share one bare repository, so
many pods on one repository cost one fetch. The driver fetches at the
interval in `pull`. When the ref has new commits, the driver writes
only the files that changed between the old commit and the new one.
It replaces each of them with one rename, so a reader sees either the
old or the new content of a file, never a partial write. A file that
did not change keeps its inode, so a pod that watches its files with
inotify sees an event only for a file that changed. [How a volume
follows its repository](https://liken.sh/git/docs/concepts/how-a-volume-follows-its-repository/#what-a-new-commit-writes)
says when the driver writes every file again.

The [attributes reference](https://liken.sh/git/docs/reference/attributes/) lists every
attribute, its values, and its default.

## A claim on a repository

A workload that takes its storage from a `PersistentVolumeClaim`
cannot use an inline volume. For that workload, create a static
`PersistentVolume` with the access mode `ReadOnlyMany` and bind a claim
to it. The `volumeAttributes` are the same as in the inline form.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: franchises
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadOnlyMany]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: ""
  csi:
    driver: git.liken.sh
    volumeHandle: franchises
    readOnly: true
    volumeAttributes:
      url: https://tangled.org/guid.foo/fiction-franchises
      ref: main
      pull: 5m
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: franchises
  namespace: default
spec:
  accessModes: [ReadOnlyMany]
  storageClassName: ""
  volumeName: franchises
  resources: {requests: {storage: 1Gi}}
```

The pod must set `readOnly: true` on the volume that uses the claim.
Without it, the container runtime mounts the volume read-write, even
though the driver's own mount is read-only. So the driver refuses to
publish the volume to a pod that does not set it, and posts a
`GitVolumeRefused` event on the pod.

```yaml
volumes:
  - name: franchises
    persistentVolumeClaim:
      claimName: franchises
      readOnly: true
```

On each node, the driver checks out one tree for the claim, mounts it
into every pod on that node that uses the claim, and keeps it until the
last of those pods stops. For a private repository, set the same
`Secret` in both `nodeStageSecretRef` and `nodePublishSecretRef`, with
the keys that the inline form uses. [Private
repositories](#private-repositories) gives the reason. The driver
ignores a `VolumeAttributesClass` on a read-only claim, because a
read-only volume never commits or pushes.

## Part of a repository

Set `paths` to check out only some directories and files of the
repository. The node then writes only those files for the volume, and
a commit that changes nothing inside them writes no file in the tree.
This inline volume reads the deploy bases of two storage drivers and
one file from the `liken` repository:

```yaml
volumes:
  - name: manifests
    csi:
      driver: git.liken.sh
      readOnly: true
      volumeAttributes:
        url: https://github.com/liken-sh/liken.git
        ref: main
        depth: "1"
        paths: |
          git-csi-driver/deploy
          per-node-csi-driver/deploy
          brand/voice.md
```

The mount contains `git-csi-driver/deploy/`,
`per-node-csi-driver/deploy/`, and `brand/voice.md`, each at the same
place as in the repository, and nothing else. `paths` does not move a
directory to the root of the mount. To mount one directory at the
container's `mountPath`, also set `subPath` on the `volumeMount`, such
as `subPath: git-csi-driver/deploy`.

`paths` limits the checkout, and not the fetch. The node's bare
repository of the URL has every file of each commit that it fetched,
and every volume of the URL on the node shares it. `depth: "1"` limits
the first fetch to the newest commit, so the bare repository of a
large repository stays small too. The value is in quotes, because
every attribute is a string.

When a push changes only files outside the entries, the webhook still
demands a pull, and the driver still fetches. The tree moves to the
new commit and no file in it changes. The [attributes
reference](https://liken.sh/git/docs/reference/attributes/#the-paths-attribute) has the
rules for an entry and the values that the driver refuses.

### An entry that a commit lacks

A commit can have no file or directory at an entry, for example after
a person renames the directory upstream. The volume then does not move
to that commit.

At the first stage, the driver refuses the volume with
`FailedPrecondition`, and the pod stays in `ContainerCreating`. The
message names the entry and the commit, such as
`paths: commit 3f2a1c9 of main has no "brand/voice.md"`. An inline
volume posts it as `GitVolumeRefused` on the pod, and a read-only
claim posts it on the claim. The kubelet retries the mount, so the pod
starts when the ref reaches a commit that has every entry. With
`offline: allowStale`, the commit can be the node's old copy from
before a failed fetch, and the message names the failed fetch too.

After the volume is mounted, a new commit that lacks an entry leaves
the tree at its current commit, and the pods keep reading the files of
that commit. The driver posts one `GitVolumePathMissing` warning, such
as `Commit 9b8e7d6 of main has no "brand/voice.md", so the tree stays
at 3f2a1c9`. The gauge `git_csi_volume_abnormal` is 1. A pull that
finds the ref still at that commit changes nothing and counts as a
successful pull, so it starts no retry. The volume keeps pulling on
its schedule. When the ref
reaches a commit with every entry, the tree moves to that commit, and
the driver posts a `Normal` `GitVolumePathFound` event. The driver
keeps the fault in memory, so after a restart it posts
`GitVolumePathMissing` again at its next pull.

To follow the new layout of the repository, change the entries. For an
inline volume, change the pod spec. For a claim, create the
`PersistentVolume` and the claim again, because the `csi` block of a
`PersistentVolume` cannot change.

## Pulling on demand

`pull` sets when a volume fetches new commits. With a duration such as
`5m`, the volume fetches on that interval and also whenever a pull is
demanded. With `on-demand`, it fetches only when a pull is demanded.
With `never`, it keeps the commit it first checked out and ignores
demands.

You demand a pull by setting the annotation
`git.liken.sh/pull-requested-at` on the `PersistentVolume` to the
current time in RFC 3339. The webhook writes the same annotation, and
you can write it yourself:

```console
kubectl annotate pv franchises git.liken.sh/pull-requested-at="$(date -u +%FT%TZ)" --overwrite
```

Each node that mounts the volume compares the annotation's time with
the start of its last successful fetch. It pulls unless the demand is
more than a minute older than that fetch. The minute allows for a
writer whose clock is behind the node's. The annotation stays on the
`PersistentVolume` after the pull, and the node does not pull again for
the same value. A value that is not a valid time is logged and ignored.

A pull for a demand updates every volume on the node that uses the same
URL. The driver keeps pulls of one repository at least
`--demand-min-interval` apart, ten seconds by default, and demands that
arrive inside that interval cause one pull when it ends. When the fetch
fails, the node retries after `--demand-min-interval`, doubles the wait
after each failure up to five minutes, and adds a random part to each
wait so that the nodes do not all fetch at once when the remote comes
back.

When the driver restarts, it pulls every volume whose `pull` is not
`never`, because it does not remember a demand that arrived while it
was down. A volume of a private repository pulls when the kubelet's next
publish call sends its credential.

An inline volume has no `PersistentVolume` to annotate, so the driver
refuses an inline volume with `pull: on-demand`, and the pod's events
give the reason. A writeable volume ignores demands, because only the
application changes a mounted writeable tree.

## Webhooks

A forge can send an HTTP request on every push, and the driver's
controller turns that request into a demand for a pull. You need a
`Secret`, the `webhookSecret` attribute, an `Ingress`, and a webhook on
the forge.

The `Secret` is in the claim's namespace and has one key, `secret`. Its
value is the string that you enter in the forge's webhook form:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: franchises-webhook
  namespace: sites
stringData:
  secret: <the string you gave the forge>
```

The `PersistentVolume` names that `Secret` in `webhookSecret`, next to
`url` and `ref`:

```yaml
spec:
  csi:
    driver: git.liken.sh
    volumeAttributes:
      url: https://code.example.com/data/franchises.git
      ref: main
      pull: on-demand
      webhookSecret: franchises-webhook
```

The kustomize base from the [install guide](https://liken.sh/git/docs/guides/install/) creates a
`Service` named `git-csi-driver-webhook` in the driver's namespace, on
port 80. Create an `Ingress` with TLS in front of
it, and give the forge the URL `https://<your host>/webhook/<namespace>/<name>`,
where `<namespace>` and `<name>` are those of the `Secret`. For the
example above, the path is `/webhook/sites/franchises-webhook`. On
GitHub, GitLab, Gitea, and Forgejo, choose the push event and enter the
same string in the secret field.

For each push, the controller reads that `Secret` and verifies the
request's signature with it. Then it writes the demand annotation on
every read-only `PersistentVolume` that has this `Secret` in
`webhookSecret`, is bound to a claim in the same namespace, and uses the
repository and ref of the push. So a push signed with one namespace's
`Secret` never marks a volume in another namespace. The response body
gives the number of volumes marked, and `marked 0` means that no volume
matched the repository and ref.

| Status | Meaning |
|---|---|
| `202` | The request is verified. The body is `marked <count>`. |
| `401` | The path does not name a `Secret` that has a `secret` key, the request has no signature that the controller recognizes, or the signature is wrong. |
| `400` | The body is not the JSON of a push, or it is larger than 1 MiB. |
| `500` | The controller could not read the cluster. Send the push again. |

The controller compares repositories by host and path. It ignores the
scheme, the user, the port, and a trailing `.git`, so a volume that
clones over `ssh://` matches a forge that reports an `https://` URL. It
compares the ref of the push with `refs/heads/<ref>` and
`refs/tags/<ref>`.

A push that arrives while the controller restarts is lost, so the
controller marks every read-only volume when it starts. A writeable
volume ignores demands, and an inline volume has no `PersistentVolume`
to mark, so the driver refuses `webhookSecret` on both.

The controller writes one log line per request, with the `Secret`, the
forge, the ref, and the count. Its metrics port exports the counters
`git_csi_webhook_requests_total`, by result, and
`git_csi_webhook_marked_total`.

## When the remote is unreachable

By default, the driver refuses a volume when the fetch fails at pod
start, and the pod stays in `ContainerCreating` with the reason in its
events. With `offline: allowStale`, the driver mounts the node's last
copy of the ref instead, and the gauge `git_csi_volume_abnormal` and the
log report the fetch error until a fetch succeeds. With either setting,
the driver refuses a volume whose repository the node has never fetched,
because it has no copy to mount.

## Private repositories

Put the credential in a `Secret` in the pod's namespace and name it in
`nodePublishSecretRef`. The `Secret` takes one of two forms:

- `ssh-privatekey`, for an SSH URL, with an optional `known_hosts`. With
  `known_hosts`, the host key must match one of its entries. Without it,
  the driver accepts the first host key that it sees and refuses a later
  change of that key.
- `token`, for an HTTPS URL, with an optional `username`. The default
  username is `git`.

```yaml
    - name: data
      csi:
        driver: git.liken.sh
        readOnly: true
        volumeAttributes:
          url: git@example.com:data/private.git
        nodePublishSecretRef:
          name: data-deploy-key
```

The driver keeps the credential in memory. It writes it to a file on
the node only while a git command runs, and never puts the token on a
command line.

The kubelet calls the driver's publish again for every mounted volume on
each pod sync, about once a minute, and reads the `Secret` again for
each call. So after the driver restarts, for example in an upgrade, it
has no credential until the next of those calls. Then it fetches at
once, and the pod does not restart. A rotated `Secret` reaches the
driver the same way, and the driver fetches with the new key at once
instead of waiting for its retry timer. Each volume fetches with its own
credential, so two volumes of one repository each wait for their own
publish call.

For a claim, the stage call fetches with `nodeStageSecretRef`, and the
publish calls send `nodePublishSecretRef`, so set the same `Secret` in
both. The driver refuses a publish whose `Secret` differs
from the one the volume was staged with. A `PersistentVolume` that has
`nodeStageSecretRef` and no `nodePublishSecretRef` works until the
driver restarts. After that, the driver cannot fetch for it until every
pod on the node that mounts it has stopped. The driver posts
`GitVolumeNoPublishSecret` on the pods and the claim when it publishes
such a volume. The `csi` block cannot change, so to fix it, stop the
pods that mount the claim, delete the claim and the `PersistentVolume`,
create both again with both references, and start the pods. Do this
before you upgrade the driver, because an upgrade restarts it.

## What the driver does not serve

A volume is a checkout of one ref of one repository. The directory of a
submodule is empty, and a Git LFS file appears as its pointer file, not
as the content that the pointer names.

## What the driver reports

The driver posts an `Event` on the pod when it refuses a mount, when it
mounts a stale copy, when a fetch fails after one that worked, when
the first fetch succeeds after a failure, when the ref's commit lacks
an entry of `paths`, and when the tree reaches a commit with every
entry again. `kubectl describe pod` shows
them. The kubelet retries a refused mount, and the repeats show as one
line with a count, such as `(x37 over 1h)`. For a read-only claim, the
driver posts each event on every pod that mounts the claim and on the
claim itself, so `kubectl describe pvc` shows them too.

The node plugin's gauge `git_csi_volume_abnormal`, labeled with the
pod's namespace and the volume, is 1 after a stale publish and after a
failed fetch, until the next fetch succeeds. It is also 1 while the
ref's commit lacks an entry of `paths`. The node plugin logs a line
when a volume's health changes. The line ends with the time of the last
demand for the volume and the time of its last pull, so it shows
whether a demand arrived and whether the pull that followed worked. The
counter `git_csi_demanded_pulls_total`, with the same labels, counts the
pulls that demands caused.
