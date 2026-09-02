# The catalog pod

Plan 28. One standing pod per namespace holds the durable catalog, and
every scanner becomes a `Job` that holds no copy. This is the first
build of [plan 27](27-enrichment.md), and it is a change to what plans
02, 04, and 15 built.

## The problem

Each `Library` runs a scanner pod that never exits. It walks the volume
once, then waits for webhooks and a timer. The pod stays up for one
reason: its Corrosion sidecar holds the durable copy of the catalog on
a `Library`-owned claim, and if the pod left, so would the copy. The
walk itself has nothing to do all day.

Plan 27 adds enrichers, and each one is a short piece of work: read a
gap, ask a provider, write a file. A standing pod per concern per
namespace is twenty idle pods. A `Job` per piece of work is the
Kubernetes shape for it, but a `Job` that starts its own agent pays a
full sync before its first query, and that is the peak the ingest
memory problem records.

## The contract

- **The `Catalog` owns one pod.** It runs a Corrosion agent on the
  namespace's one durable claim and nothing else. It is the standing
  member of the gossip cluster. Its HTTP API, `/v1/queries`,
  `/v1/transactions`, and `/v1/subscriptions`, is on a `Service` in
  the namespace, and nothing outside the namespace reaches it.
- **A scan is a `Job`.** It mounts the library read-only, walks it,
  and writes rows through the catalog pod's API. It runs no sidecar
  and holds no claim. The walk, the prune, and the report are the same
  code as today with a different API address. The `seen` table stays
  a local table, created through the API on the catalog pod.
- **The operator creates every `Job`.** A webhook creates a scan `Job`
  for one folder. A `CronJob` per `Library` runs the full walk on the
  library's interval. The operator runs at most one full walk per
  `Library` at a time, and a folder scan may run beside one.
- **The `Library`-owned claim goes away.** The `Catalog` sizes one
  claim, its own, from `spec.storage` as today, or binds an existing
  claim when `spec.storage.claimName` names one, the way a `Library`
  binds its volume. `Library` status keeps its counts and phase, read
  through the catalog pod after each `Job`.
- **Screens do not change.** A screen pod keeps its own agent and its
  own copy on an `emptyDir`, and gossips with the catalog pod. A
  screen reads its own file.
- **The webhook `Service` moves to the operator.** The scanner pod is
  gone, so the address in `Library` status names an endpoint the
  operator serves. The operator turns the call into a `Job`.

## Failure

When the catalog pod is down, every `Job` fails on its first write and
Kubernetes retries it with backoff. Screens keep reading their own
copies. When the catalog pod's node is down and the claim is
`ReadWriteOnce` on that node, the pod waits for the node. That is the
one pod to look at when nothing scans.

## The change on a running cluster

The change ships against a fresh database, the way plan 20 did, with
a versioned claim name on the `Catalog` and a full walk of every
`Library` after the roll. The old `Library`-owned claims are deleted
with their scanner pods once the new catalog reports every library
in full.

## Proof

On `liken-1`: the roll, a full walk of the movies and series libraries
through `Job`s, a folder scan from a webhook, a screen that comes up
against the catalog pod, and a kill of the catalog pod while a walk
runs. Recorded: the time to the first full report against the number
plan 04 recorded, the catalog pod's resident memory after the walk and
after a restart, and what the killed walk left in the catalog.

## What is set aside

A `Job` with its own agent and claim, one copy per worker. Every run
pays a full sync, minutes and hundreds of megabytes, before its first
query. Set aside without a build.

A standing pod per concern. It is the shape the scanner has today, and
plan 27 adds twenty of them. Set aside for the `Job`.

## What is not decided

The grace period the catalog pod needs, given the slow shutdown problem. Whether
the ingest peak on the one standing pod needs the restart trick plan 06
uses on screens.
