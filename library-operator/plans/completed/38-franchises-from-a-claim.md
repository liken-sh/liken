# 38, Franchises from a claim

Built, and proved on `liken-1` on 2026-09-06 with development build
`2026.09.04-003-dev-029-2b5e3245` beside the git CSI driver's plan 07
build. The old `Library` and its claim were deleted, a `ReadOnlyMany`
claim on the public franchises repository and an NFS claim for the art
took their places, and the new `Library` reported `Bound` over a `csi`
volume and `Ready` with 34 titles. The first scan pod walked all 34
franchises in one second and then failed on the catalog echo: its
catalog sidecar started on a new claim and synced the whole namespace
catalog for 2 minutes and 5 seconds, past the 2 minute echo timeout,
and the Job's second pod completed. That first-scan cost is open, and
it is the catalog's and not this plan's. The screen pod mounts the art
claim read-only.

## The problem

A `Library` of kind `franchises` is the one kind that names no volume
for its truth. It names a git repository under `spec.storage.git`, and
each scan Job clones that repository with a git binary the image carries
for this one kind. The clone gave the operator a second storage shape, a
second printer column, a `status.commit` that no other kind reports, an
image with git and its runtime in it, and a scanner that reaches a forge
on its own.

The [git CSI driver](https://git.liken.sh/) serves a repository as a
volume, and its plan 07 serves one as a `ReadOnlyMany` claim that
follows its ref. With that claim, a franchises library is a library like
the others: a claim that holds its truth, which the scanner walks.

## The design

### Every library names a claim for its truth

`spec.storage.git` is removed. `spec.storage.claim` names the checkout
for a franchises library, the way it names the media for a movies or
series library, and `spec.storage.root` is a directory inside it. How
the checkout arrives on the claim is the cluster owner's choice. The
git CSI driver is the way this project drills, and any volume that
holds one directory per franchise serves.

The `Repository` printer column goes with the field. The CRD rule that
tied `storage.git` to the kind goes with it, and the kind rule that
remains is the settings rule. A `Library` is immutable in its storage
and its kind today, so a franchises library that exists under the old
shape is deleted and created again under the new one.

### The art has a claim of its own

The scan downloads the art each `franchise.yaml` links to, and the
checkout is read-only, so the art needs a writeable place. The
franchises settings block names it:

```yaml
spec:
  storage:
    claim: franchises
  kind: franchises
  franchises:
    art:
      claim: franchises-art
```

`spec.franchises.art.claim` is required for the kind, and the CRD says
so with a rule on the block. It is a claim in the same namespace, and
the scan Job mounts it writeable at a mount of its own while it mounts
the storage claim read-only, like every other scanner. The art lands
under the same directory names the checkout uses, with the
`.liken/art.yaml` ledger beside it, exactly as today.

The art claim is the claim a screen reads this library's art from, and
the claim a `claim://` reference names for this kind. One method on the
spec answers "which claim holds the files a screen reads", and the
screen pod, the play request, and the enrich Job ask it, so a franchises
library's screen mounts the art and not the checkout.

### A scan walks the claim, and no more

The scanner reads the checkout at the library mount under
`spec.storage.root`, and the art root is the art mount. The clone, the
`LIBRARY_GIT_URL` and `LIBRARY_GIT_REF` variables, the checkout
`emptyDir`, and the git stages of the `Dockerfile` are removed, and the
image carries no git.

`status.commit` and the `commit` of a run are removed. They existed so a
scan on a schedule could cost one clone and nothing else. The checkout
is a mounted volume now, the files are a few hundred kilobytes, and the
walk reads them in well under a second, so every scan walks, the way
every other kind's scan does. The art ledger keeps a scan from reading a
link twice, which is the one cost that was worth avoiding.

The failure shape follows the other kinds. A claim the kubelet cannot
mount holds the Job at `ContainerCreating`, and the library's `Bound`
and `Ready` conditions say so, because a franchises library now has a
claim for `Bound` to report on and a `status.volume` to carry. A
checkout the walk could not read in full prunes nothing, which is the
incomplete-walk guard every kind has.

### The reference pages

The CRD descriptions for `storage`, `storage.claim`, `kind`,
`franchises`, `franchises.image`, `status.volume`, `status.phase`,
`status.conditions`, and `status.runs` say what is true after this
plan, and `reference/libraries.md` regenerates from them. The
franchises reference page describes a library over a claim and points
at the git CSI driver's read-only guide as the way to serve one from a
repository.

## Considered and set aside

- **The art in the repository.** The repository would hold the bytes
  and the driver would push them. The repository is public and the art
  belongs to whoever published it, and a writeable volume is
  `ReadWriteOncePod` where the scan and the screens all read the art
  at once. The art stays derived, on a claim of its own.
- **Reading the commit through the driver.** The driver publishes a
  tree and no `.git`, by design, so nothing on the claim names a commit.
  A digest of the files would stand in for one. The walk is cheap
  enough that neither is needed.
- **The art claim as `spec.storage.claim`, and the checkout under
  `spec.franchises`.** It would keep the screen's mount where it is.
  It would also make the one kind whose `storage` is not its truth, and
  `status.volume` would report the art. The truth is the storage,
  for every kind.

## Proof

On `liken-1`, with the git CSI driver at its plan 07 build and this
operator at its development build:

1. Delete the franchises `Library` and its volume manifests. The
   cleanup Job takes the library's rows out of the catalog.
2. Apply a `ReadOnlyMany` `PersistentVolume` and claim on
   `https://tangled.org/guid.foo/fiction-franchises` through
   `git.liken.sh`, an NFS `PersistentVolume` and claim for the art, and
   the `Library` under the new shape.
3. The `Library` reports `Bound` with a `csi` volume and `Ready`. A
   scan Job runs to completion, and `status.titles` reads the number of
   franchise directories the repository holds.
4. The art ledger on the NFS export is what the last scan under the
   old shape wrote, so the scan downloads no art it already holds.
5. The franchises screen on the LG draws the wall from the new rows.

`make test` holds its floor, and the image carries no `git` binary.
