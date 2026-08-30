# The Library resource

Plan 02. The operator's first resource, and the loop that reconciles it.
At the end of this plan a `Library` can be created, validated, bound to
a volume, and reported on, and the operator runs a scanner pod for it
that does nothing yet.

## The problem

Everything else in the design depends on a declaration of "this volume
holds movies". That declaration must say which volume, which root
directory, which kind, and what the kind needs to know. It must say it
in a shape that grows one kind at a time without changes to the kinds
already there. And it must report back: how many titles, how many
folders the scanner could not identify, when it last walked, and whether
it is healthy.

## The contract

`Library` is namespaced, in the group `library.liken.sh`. A namespace
may hold many, of any mix of kinds.

The spec has three parts.

- **Storage.** A `Library` names a `PersistentVolumeClaim` in its
  namespace and a root path inside it. The claim can be any volume the
  cluster can mount. The scanner mounts it read-only. The operator reads
  the `PersistentVolume` behind the claim, because the playback plan
  needs to know the volume's kind to build a media reference a `Player`
  accepts. One volume may hold several libraries, each with its own
  root.
- **Kind.** One discriminator field names the kind, and one typed
  settings block per kind holds that kind's settings, the way a `Volume`
  names one source. A CEL rule requires the block that matches the kind
  and forbids the others. Plan 04 defines the movies block and the series
  block. This plan defines the discriminator, the validation, and the
  rule that a new kind is a new block. A settings block may name the
  scanner image to run, so a person can supply their own scanner for a
  kind the project ships and for kinds it does not.
- **Sources.** An ordered list of metadata provider names. The
  enrichment plan reads it. It is in the schema from the start so that
  plan changes no shape.

The status reports what the scanner reports. It has the count of titles,
the count of folders with no sidecar and no confident parse, the time of
the last full walk, the time of the last change applied, the scanner
pod's name, and conditions. The scanner does not write status itself. It
publishes a retained report on `media-operator`'s bus, under this
operator's own topic base, and the operator folds that report into the
status, as `media-operator` folds a `Play`'s report from its sidecar. So
a scanner needs no API credential and no RBAC.

The operator reconciles each `Library` into one scanner pod. The
`Library` owns the pod, so a delete removes it, and the operator rolls
the pod when the template's hash changes, as `media-operator` rolls its
standing pods. The pod has two containers: the scanner for the kind, and
the Corrosion sidecar from plan 03. In this plan the scanner container
publishes a report of zero titles, so the loop, the status, and the bus
path are proved before a parser exists.

## What was set aside

A cluster-scoped `Library`. A namespace is a boundary, and a library
is visible inside its namespace and nowhere else. The claim it mounts
is in the same namespace, as every reference this operator makes is.

A free-form settings map for kinds. It would let a kind ship without a
schema change, and it would stop the API server from validating anything
about it. Kinds are few, and each one is a deliberate addition.

A list of screens on the `Library`. Which screens show which libraries
is an open problem. The answer is a resource of its own, so both sides
stay many-to-many.

## Proof

On `liken-1`: a `Library` of kind movies, bound to a claim over the lab's
movie volume, is created. Its status reports the volume it resolved, its
scanner pod starts with both containers, and its status folds the
zero-title report within one reconcile. A second `Library` of the same
kind in the same namespace does the same beside it. Deleting one removes
its pod and leaves the other.

## What ran

Release 2026.08.29-002, rolled to `liken-1` on 2026-08-29, and again as 2026.08.30-001 after the kind was renamed from films to movies. Two
`Library` objects of kind movies in one namespace, both over one
`ReadOnlyMany` claim on the lab's NFS movie export, one at `/` and one
at `/Sci-Fi`. Each reported `Bound` and then `Ready` 8 s after it was
created, with the volume's name, `nfs`, the server, and the export path
in its status, the scanner pod's name, and the zero-title report's
counts and times. Each scanner pod ran two containers, mounted the
claim read-only at `/library`, and mounted no `ServiceAccount` token.
Deleting one `Library` removed its pod within 10 s and left the other
`Ready`.

Two things the drill found. The operator's first list after the CRD is
created in the same apply answers 429 while the API server initializes
the resource's storage, so the pod exits once and the kubelet restarts
it; the second start lists cleanly. And a deleted `Library`'s retained
report and availability stay on the broker, because nothing clears
them yet; the scanner plan owns that reclaim.
