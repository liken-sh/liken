---
title: Mount a repository read-only
weight: 20
---

A read-only volume has two forms. The inline form is a CSI volume in
the pod spec. It needs no `PersistentVolume` and no claim, so any pod in
any namespace can mount any repository the node can reach. The claim
form, below, is for a workload that names its storage as a claim.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: reader
spec:
  containers:
    - name: reader
      image: debian:12-slim
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

The pod sees a plain directory with the files of the ref and no `.git`.
Every volume of the same URL on a node shares one bare repository, so
ten pods on one repository cost one fetch. The driver fetches every
`pull`, and when the ref moved it replaces the files under the mount one
by one, so a reader sees the old file or the new one and never a partial
write.

The [attributes reference](../../reference/attributes/) lists every
attribute, its values, and its default.

## A claim on a repository

A workload that names its storage as a `PersistentVolumeClaim` cannot
mount an inline volume. For that workload, the driver serves a
repository as a static `PersistentVolume` with the access mode
`ReadOnlyMany`, and a claim binds it. The attributes are the ones the
inline form takes.

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

A pod mounts the claim with `readOnly: true` on its volume. The
container runtime binds a volume into a pod read-write unless the pod
asks for read-only, whatever the driver's own mount says, so the driver
refuses a pod that does not ask, with `GitVolumeRefused` in the pod's
events.

```yaml
volumes:
  - name: franchises
    persistentVolumeClaim:
      claimName: franchises
      readOnly: true
```

Many pods on one node publish one tree, each at its own mount, and the
driver keeps the tree until the last of them stops. A private repository
names its `Secret` through `nodeStageSecretRef`, with the keys the inline
form takes through `nodePublishSecretRef`. The driver ignores a
`VolumeAttributesClass` on such a claim, because a read-only volume
commits nothing and pushes nothing.

## Pulling on demand

`pull` says when a volume looks for a new commit. It takes one of
three values.

| Value | Meaning |
|---|---|
| `never` | No timer and no demand. The volume holds the commit it staged for its whole life. |
| `on-demand` | No timer. The volume pulls only when something demands it. |
| A duration such as `5m` | The volume pulls at least that often, and it pulls when something demands it. |

A demand is an annotation on the `PersistentVolume`. Any value the
driver has not acted on yet is a demand, and the convention is the
time:

```console
kubectl annotate pv franchises git.liken.sh/pull-requested-at="$(date -u +%FT%TZ)" --overwrite
```

The node that holds the volume pulls at once, and every volume of the
same URL on that node moves with it. Twenty demands inside
`--demand-min-interval`, which defaults to ten seconds, cost one pull
at the end of the interval, not twenty. A driver that restarts pulls
once for every volume that is not `never`, because a demand that came
while it was down is lost.

An inline volume has no `PersistentVolume`, so nothing can demand it.
An inline volume with `pull: on-demand` is refused, and the pod's
events say why. A writeable volume ignores a demand, because only the
application changes a mounted writeable tree.

## Webhooks

A forge sends an HTTP request on every push, and the controller turns
that request into a demand. Four things make it work: a `Secret`, an
attribute, an `Ingress`, and the webhook on the forge.

The `Secret` lives in the claim's namespace and holds one key,
`secret`, whose value is the string you type into the forge's webhook
form:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: franchises-webhook
  namespace: sites
stringData:
  secret: <the string you gave the forge>
```

The `PersistentVolume` names that `Secret` with `webhookSecret`, beside
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

The base holds a `Service` named `git-csi-driver-webhook` in the
driver's namespace, on port 80. Write an `Ingress` in front of it, with
TLS, and give the forge the URL
`https://<your host>/webhook/<namespace>/<name>`, here
`/webhook/sites/franchises-webhook`. On GitHub, GitLab, Gitea, and
Forgejo, choose the push event and paste the same string into the
secret field.

On every push the controller reads that one `Secret`, verifies the
request against it, and demands a pull on every read-only volume that
names the `Secret`, is bound to a claim in that namespace, and follows
the repository and ref the push names. A push that verifies against
one namespace's `Secret` never reaches another namespace's volumes.
The answer names how many volumes it marked, so `marked 0` after a
push means the URL or the ref matched nothing.

| Answer | Meaning |
|---|---|
| `202` | The request verified. The body reads `marked <count>`. |
| `401` | The path names no `Secret`, the request carries no signature the controller checks, or the signature is wrong. |
| `400` | The body is not the JSON of a push, or it is over 1 MiB. |
| `500` | The controller could not read the cluster. Try the push again. |

The controller compares repositories by host and path, with the
scheme, the user, the port, and a trailing `.git` removed, so a volume
that clones over `ssh://` matches a forge that advertises `https://`.
It compares the ref against `refs/heads/<ref>` and `refs/tags/<ref>`.

A webhook that arrives while the controller restarts is lost, so the
controller demands a pull on every read-only volume when it starts. A
writeable volume never takes a demand, and an inline volume has no
`PersistentVolume` to mark, so `webhookSecret` is refused on both.

The controller's log writes one line per request with the `Secret`,
the forge, the ref, and the count. The counters
`git_csi_webhook_requests_total`, by result, and
`git_csi_webhook_marked_total` are on the controller's metrics port.

## When the remote is unreachable

By default a volume whose fetch fails at start is refused, and the pod
stays in `ContainerCreating` with the reason in its events. Set
`offline: allowStale` to publish the node's last copy of the ref
instead. The abnormal gauge and the log then report the fetch error
until a fetch succeeds. A repository the node has never fetched is refused
under both settings, because there is nothing to publish.

## Private repositories

Name a `Secret` in the pod's namespace with `nodePublishSecretRef`. The
driver reads two kinds of credential from it:

- `ssh-privatekey`, for an SSH URL, with an optional `known_hosts`. With
  `known_hosts`, the driver checks the host key. Without it, the driver
  accepts the first key it sees and refuses a later change.
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

The credential reaches the node's disk only for the length of one git
invocation, and the token never appears on a command line.

## What the driver does not serve

A checkout is one ref of one repository. A submodule's directory is
empty, and a Git LFS pointer file is checked out as the pointer and not
the object it names.

## What the driver reports

A refused mount, a stale publish, and a fetch that fails after one that
worked each post an `Event` on the pod. `kubectl describe pod` shows
them.
A read-only claim posts each of those on every pod it is published to
and on the claim, so `kubectl describe pvc` shows them too. The node
plugin's gauge `git_csi_volume_abnormal`, labeled by the
pod's namespace and the volume, is one after a stale publish and after
a failed fetch, until the next fetch succeeds. The log line the node
plugin writes when a volume's health turns ends with when a demand
last named the volume and when it last pulled, so one line says
whether a demand arrived and whether the pull that followed worked.
The counter `git_csi_demanded_pulls_total`, with the same labels,
counts the pulls a demand started.
