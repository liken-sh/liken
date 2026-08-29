# The Library resource

Plan 02. The operator's one resource, and the loop that reconciles
it. At the end of this plan a `Library` can be created, validated,
bound to storage, and reported on, and the operator runs a scanner
pod for it that does nothing yet.

## The problem

Everything else in the design hangs off a declaration of "this share
holds films". That declaration has to say which share, which root,
which kind, and what the kind needs to know, and it has to say it in
a shape that grows one kind at a time without touching the kinds
already there. It also has to report back: how many titles, how many
folders the scanner could not identify, when it last walked, and
whether it is healthy.

## The contract

`Library` is namespaced, in the group `library.liken.sh`. A namespace
may hold many, of any mix of kinds.

The spec has three parts:

- **Storage.** A `Library` binds to a `PersistentVolumeClaim` in its
  namespace, mounted read-only into its scanner. The operator reads
  the bound `PersistentVolume` behind the claim to learn the NFS
  server and export, because the browser later needs an `nfs://` URI
  for a `Play` and that is the only place the address lives. A
  library also names a root path inside the mount, so one share may
  hold several libraries.
- **Kind.** One discriminator field names the kind, and one typed
  block per kind holds that kind's settings, the way a `Volume`
  names one source. A CEL rule requires the block matching the kind
  and forbids the others. The films block and the series block are
  defined by plan 04; this plan defines the discriminator, the
  validation, and the rule that a new kind is a new block. A kind
  block may name the scanner image to run, so a person can bring
  their own scanner for a kind the project ships and for kinds it
  does not.
- **Sources.** An ordered list of metadata provider names, used by
  the enrichment plan and ignored until then. It is in the schema
  from the start so that plan changes no shape.

The status reports what the scanner reports: the count of titles,
the count of folders that had no sidecar and no confident parse, the
time of the last full walk and of the last change applied, the
scanner pod's name, and conditions. The scanner never writes status
itself. It publishes a retained report on `media-operator`'s bus, on
a topic under this operator's own base, and the operator folds that
report into the status, the way `media-operator` folds a `Play`'s
report from its sidecar. So a scanner needs no API credential and no
RBAC.

The operator reconciles each `Library` into one scanner pod, owned by
the `Library` so a delete tears it down, and rolled when the pod
template's hash changes, the way `media-operator` rolls its standing
pods. The pod holds two containers: the scanner for the kind, and the
Corrosion sidecar from plan 03. In this plan the scanner container is
a placeholder that publishes a report of zero titles, so the loop,
the status, and the bus path are proved before a parser exists.

## What was set aside

A cluster-scoped `Library`. Shares and secrets are namespaced, and a
library belongs with the claim it mounts.

A free-form settings map for kinds. It would let a kind ship without
a schema change, and it would take the API server out of validating
anything about it. Kinds are few and each one is a deliberate
addition.

A `Library` per screen, or a list of screens on the `Library`. Which
screens show which libraries is the shelves plan's concern, and it
lives in a resource of its own so both sides stay many-to-many.

## Proof

On `liken-1`: a `Library` of kind films bound to a claim over the
lab's film share is created, its status reports the NFS source it
resolved, its scanner pod starts with both containers, and its
status folds the placeholder report within one reconcile. A second
`Library` of the same kind in the same namespace does the same beside
it. Deleting one removes its pod and leaves the other.
