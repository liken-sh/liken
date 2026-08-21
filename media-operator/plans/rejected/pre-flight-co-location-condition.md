# A pre-flight co-location condition on a Player

The founding design asks for one. A `Player` whose devices cannot
share one machine should get a status condition that says so "before
anyone plays to it" ([00-design.md](../00-design.md), the section
"One machine owns a player's devices"). This document rejects that
condition. The juice is not worth the squeeze.

## What already answers the question

The scheduler already answers it, one step later and with no new
code. A `Play` builds a single `ResourceClaim` whose named requests
are the `Player`'s roles, and the set allocates as a unit: one
machine satisfies every request, or the pod parks `Pending`. The
operator folds that into the `Play` status as "no machine holds every
claimed device". So a person who plays to an unsatisfiable `Player`
learns why within one reconcile, in the words of the resource they
just created.

The design's word was "before". The only thing a pre-flight condition
adds is learning the same fact without first creating a `Play`. That
is the whole of the value, and it is small.

## What it would cost

A `Player` holds no claims and runs no pod, by design, so the operator
has no allocation result to read ahead of a `Play`. The allocation
decision lives in the scheduler's DRA allocator, and it runs only when
a pod references the claim. To answer the question early, the operator
must reach for data it does not touch today and re-derive a decision
the scheduler owns.

Three ways to get there, and none pays for the value above:

* **Re-derive allocatability from `ResourceSlices`.** Read every
  slice, evaluate each role's CEL selector against every device, group
  the matches by node, and honor counts and allocation modes. This
  re-implements a slice of the scheduler and drifts from it. It is
  wrong in both directions: it calls a `Player` satisfiable while a
  live claim already holds one of its devices, and unsatisfiable over
  a selector nuance the real allocator reads differently.
* **Probe with a real allocation.** A `ResourceClaim` allocates only
  through a consuming pod, so this needs a standing pod per `Player`.
  That is the "always-on player pods" the design already set aside: an
  idle pod holds the TV a console needs.
* **Prove only impossibility.** Catch the cases the operator can know
  for certain: a `class` that names no `DeviceClass`, a `selector`
  that does not compile, or an empty intersection of the nodes that
  publish each role. This is honest but partial. It proves a `Player`
  cannot work, never that it can, so its absence is not a promise that
  a `Play` will schedule. It still adds a `ResourceSlice` watch, read
  access to `resource.k8s.io`, and a `Player` reconcile trigger on
  hardware coming and going, none of which the operator carries today.

## The decision

The cheapest honest option, impossibility-only, still adds a watch, a
permission, and a reconcile path to save one create-and-watch cycle
that already reports its own failure clearly. The operator stays out
of the allocation business the scheduler owns, and the reactive `Play`
message stays the one place a person reads why a unit will not play.

If a future frontend needs to gray out an unplayable `Player` in a
menu, the impossibility-only check is where to start, and this
document is the record of why it was not built first.
