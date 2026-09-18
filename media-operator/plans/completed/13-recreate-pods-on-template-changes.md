# 13, Recreate long-running pods when templates change

The operator owns two kinds of standing pods: the idle pod per
`Player` and the reader pod per `Remote`. Both are created when they
are missing and never touched again. So an operator release leaves
every standing pod on the old image until a person deletes it, and a
`Player` or `Remote` edit that changes the pod's environment or its
claim's selector leaves a pod that no longer matches its spec. Plan
12's rollout showed both: the idle pods and the reader pod all needed
manual deletes to pick up the release.

The operator already builds the desired pod and claim on every pass.
This plan records a hash of each generated template, detects template
changes, and replaces the stale object. An operator release or spec
edit that changes a pod's template recreates that long-running pod.

## The template hash

A `Deployment` detects a stale pod by stamping a hash of the template
it built (`pod-template-hash`) and comparing it, never by comparing live
specs, because the API server defaults fields the builder never set.
The operator does the same with one annotation,
`media.liken.sh/template-hash`, on each object it stands up:

* The claim carries the hash of the claim spec the operator built.
* The pod carries the hash of the pod spec the operator built.

On every pass, for each standing pod and its claim:

1. Hash what the pass would build now.
2. If the claim's hash differs, delete the pod and the claim. Both
   are stale: a claim is immutable, and the pod holds its allocation.
3. If only the pod's hash differs, delete the pod alone. The claim
   and its allocation survive, so an image-only release does not cost
   a sleeping controller the allocation it holds, and the reader pod
   comes back running instead of `Pending`.
4. The next pass finds the objects missing and recreates them, the
   path that already exists. An object with a deletion timestamp
   counts as still present and is left alone, so one divergence
   causes one delete and not one per pass.

A roll interrupts. Recreating a reader pod drops controller input for
a few seconds, and recreating an idle pod blinks the idle screen
once. An upgrade therefore includes this brief interruption.

## The delete verbs

The operator already has delete permission on pods and claims, granted
for the path that recreates a reshaped playback pod. This plan uses
those permissions for the standing pairs and adds none. The owner
references stay, and they remain the whole teardown for a deleted
`Player` or `Remote`.

## Considered and set aside

* **A `Deployment` per `Player` and per `Remote`.** A workload template
  references
  claims through a `ResourceClaimTemplate`, which mints a fresh claim
  per pod and gives up the standing claim. And a selector edit still
  needs the operator to delete and recreate the immutable claim,
  which a `Deployment` cannot do, so the operator would carry a
  second rolling mechanism beside the `Deployment`'s.
* **Comparing live specs instead of hashes.** The API server
  defaults fields the builder never set, so a live comparison either
  rolls forever or grows a field-by-field allowlist. The stamped
  hash compares the operator's own output across passes and nothing
  else.
* **Holding a roll while a controller holds focus on a running
  film.** A guard that defers the reader pod's roll would trade a few
  seconds of input for a delay whose duration depends on the running
  controller. Releases are deliberate and rolls are expected.

## Proof

On `liken-1`, the release of this plan is its own drill:

* Apply the release. The idle pods and the reader pod roll on their
  own, with no manual delete, and come back on the new image.
* Edit a `Player`'s idle-relevant spec. The idle pod rolls; the
  claim stays.
* A second pass after the rolls deletes nothing, because the hashes
  match.
