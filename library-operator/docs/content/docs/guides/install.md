---
title: Install the operator
weight: 10
---

# Install the operator

This guide installs `library-operator` on a
[`liken`](https://liken.sh/docs/) cluster. At the end, the operator
runs, a namespace has its catalog, and a `Library` can be declared.

You need:

* A `liken` cluster.
* The [`media-operator`](https://media.liken.sh), installed first, in
  `liken-system`. It owns the players, the plays, and the remotes, and
  its bus at `bus.liken-system.svc:1883` is where every catalog
  reports. A `Library` in a cluster with no bus reports `Offline` and
  never reaches `Ready`.
* A volume that holds the media, as a `PersistentVolumeClaim` in the
  namespace where the `Library` will be. The operator never creates
  this claim. See [The media claim](#the-media-claim) below.
* A `StorageClass` for the catalogs. The operator provisions those
  claims itself, one per catalog pod, one per scan `Job`, and one per
  screen. See [The catalog claims](#the-catalog-claims).
* `kubectl` with cluster-admin access. The base creates a
  `ClusterRole` and three `CustomResourceDefinitions`.

This operator publishes no devices and needs no `DeviceClass`. The
screen it draws claims the display through the `Player`'s own standing
claim, which `media-operator` holds.

## 1. Create the storage

### The media claim

A `Library` names an existing claim in `spec.storage.claim`. Any
volume the cluster can mount works: an NFS export, a Longhorn volume,
a local disk on a single-node cluster.

Three kinds of pod mount the claim, and they can land on different
nodes. Every scan `Job` mounts it read-only. The enrich `Job` mounts it
read-write, because it writes the `.nfo` and art files beside the
media. So the claim has to allow more than one node at once, which on
most clusters means `ReadWriteMany`:

    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: movies-pvc
      namespace: media
    spec:
      accessModes: [ReadWriteMany]
      resources:
        requests:
          storage: 500Gi
      storageClassName: nfs

A `ReadWriteOnce` claim works only when every pod that mounts it is
on the same node, which nothing in the schema enforces.

A franchises library needs a second claim for the art its scan
downloads. [Franchises](/docs/guides/franchises/) covers it.

### The catalog claims

The catalog is SQLite, and one agent writes each copy. So the operator
provisions every catalog claim as `ReadWriteOnce`, and the namespace's
`Catalog` names the class of each kind of claim:

* `spec.storage.storageClassName` for the catalog pod's claim, the
  catalog of record every other agent syncs from.
* `spec.progress.storageClassName` for the progress store's claim,
  the record of who watched what. It defaults to the catalog's class,
  and `spec.progress.size` gives the store a size of its own.
* `spec.libraries.storageClassName` for the scan and enrichment claims
  of every `Library`. It defaults to the catalog's class.
* `spec.screens.storageClassName` for both claims of every screen.

A class left empty binds to the cluster's default class.

The catalog of record and the progress store are the two claims worth
keeping, so give them a class that survives a lost node, such as
block storage from a SAN. A SQLite file on NFS can corrupt when its
node is lost, so do not use an NFS class. A `Library`'s claims are
working copies that a `Job` rebuilds from the catalog of record, and a
screen pod is pinned to the machine that holds its display, so a
node-local class such as `local-path` fits both.

## 2. Apply the manifests

The install is the kustomize base in the repository's
[`deploy/`](/deploy/kustomization.yaml) directory. Take it into a
kustomization of your own and pin `<tag>` to a release, so the install
is the same every time it is applied:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization

    namespace: liken-system

    resources:
      - https://github.com/liken-sh/library-operator//deploy?ref=<tag>

    images:
      - name: ghcr.io/liken-sh/library-operator
        newTag: <tag>

The base creates the three `CustomResourceDefinitions`, a
`ServiceAccount`, a `ClusterRole` with its binding, one `Deployment`,
and one `Service`. The `Deployment` runs one unprivileged replica with
every capability dropped. The `Service` is the address every
`Library`'s [webhook](/docs/guides/webhooks/) is reached at.

The `ClusterRole` is cluster-wide because a `Library` can be in any
namespace. Its grants are read and status writes on this operator's
own resources, read on `media-operator`'s `Player` and
`MediaPreferences`, create and patch on `Play`, patch on
`people.liken.sh`'s `Person` for one finalizer, and create and delete
on the claims, pods, `Jobs`, `CronJobs`, `Services`, and the one
`ConfigMap` per screen namespace it owns, which carries the `Person`
list every screen reads.

This site serves the same files as raw YAML, so a clone is never
needed: [`libraries-crd.yaml`](/deploy/libraries-crd.yaml),
[`catalogs-crd.yaml`](/deploy/catalogs-crd.yaml),
[`metadataproviders-crd.yaml`](/deploy/metadataproviders-crd.yaml),
[`watches-crd.yaml`](/deploy/watches-crd.yaml),
[`rbac.yaml`](/deploy/rbac.yaml), and
[`operator.yaml`](/deploy/operator.yaml).

## 3. Declare a Catalog

A `Library` is namespaced, and each namespace that holds one has a
catalog of its own. The namespace is a boundary: every `Library` in it
writes into one catalog, and a screen shows that catalog. Declare
exactly one `Catalog` in the namespace before the first `Library`. A
`Library` in a namespace with no `Catalog` waits with the reason
`NoCatalog`, and a second `Catalog` blocks both.

    apiVersion: library.liken.sh/v1alpha1
    kind: Catalog
    metadata:
      name: catalog
      namespace: media
    spec:
      storage: {}

An empty `storage` provisions a `1Gi` claim on the default class.
[Catalog](/docs/reference/catalogs/) describes every field, and
[The catalog](/docs/guides/catalog/) describes what the pod it creates
does.

## 4. Confirm it runs

    kubectl -n liken-system get pods
    kubectl -n liken-system logs deploy/library-operator

The operator's log reports its first pass and the bus it reports over:

    library.liken.sh: operating 0 libraries over bus.liken-system.svc:1883

A missing `LIBRARY_BUS_ADDRESS` or `OPERATOR_NAMESPACE` is an error at
startup, printed to the log, and the pod exits. The served
[`operator.yaml`](/deploy/operator.yaml) sets both.

Now [declare a library](/docs/guides/libraries/). Once it is `Ready`,
the listing shows its counts and its phase:

    $ kubectl -n media get libraries
    NAME     KIND     TITLES   ITEMS   FILES   WAITING   STATUS   READY   AGE
    movies   movies   0        0       0       0         Idle     True    2m

## Running a development build

Every push to the operator's main branch publishes a development
build. Its version is the most recent release plus a suffix:
`2026.09.03-007-dev-003-abcdef01` is three commits past release
`2026.09.03-007`, at commit `abcdef01`. Every image the repository
builds carries the same version, and `:latest` still names the most
recent release.

A development build has no git tag, so the manifests pin to the
commit's full sha, and the image pins to the version:

```yaml
resources:
  - https://github.com/liken-sh/library-operator//deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/library-operator
    newTag: 2026.09.03-007-dev-003-abcdef01
```

A git fetch by sha needs all forty characters; the eight in the
version are not enough. The CI run for that commit prints both lines
in its summary.

## Remove the operator

Delete every `Library` first, and wait for each one to go. A `Library`
carries a finalizer that the operator releases after a cleanup `Job`
removes its rows from the namespace's catalog. If the operator's
`Deployment` is gone, nothing runs that `Job`, and the `Library` stays
`Terminating` until a person patches the finalizer off.

    kubectl -n media delete library movies
    kubectl -n media delete catalog catalog
    kubectl delete -k https://github.com/liken-sh/library-operator//deploy?ref=<tag>

Deleting the `Catalog` deletes the catalog pod and the claim the
operator provisioned for it. Deleting the base deletes the
`CustomResourceDefinitions`, and with them every `MetadataProvider`.
The media claim and what is on it stay.
