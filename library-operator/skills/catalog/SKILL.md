---
name: catalog
description: "The Catalog resource: the SQLite database Corrosion replicates, one cluster per namespace, and how pods, Jobs, and screens read and write it. Use when declaring a Catalog, when rows do not land, or when reading the catalog by hand."
---

This skill is the guide at https://library.liken.sh/docs/guides/catalog/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# The catalog

Scanners write to the catalog, and every screen reads from it. The
catalog is a SQLite database that [Corrosion](https://github.com/superfly/corrosion)
replicates. Each namespace has one catalog cluster. Its catalog pods
are the long-running members, and every pod that reads or writes the catalog
runs another member. This guide describes the `Catalog` resource that
creates the cluster, how a screen gets its copy, and how the counts
reach a `Library`'s status.

## The Catalog resource

One `Catalog` per namespace creates the catalog pods. It sizes every
catalog claim in the namespace and owns the `Service` that the
cluster's members use to find one another:

    apiVersion: library.liken.sh/v1alpha1
    kind: Catalog
    metadata:
      name: catalog
      namespace: media
    spec:
      storage:
        size: 1Gi
        storageClassName: local-path
      screens:
        storageClassName: local-path
        artCache:
          size: 2Gi

Every member holds the whole namespace's catalog, because the cluster
gossips every row to every peer. `spec.storage.size` sets the size of
new catalog claims for the durable replicas, worker `Jobs`, and
screens. `spec.storage.claimName` names an existing claim for the
durable replicas in place of the one the operator provisions.
[Catalog](https://library.liken.sh/docs/reference/catalogs/) describes every field.

The listing shows the storage size, requested replica count, and
readiness:

    $ kubectl -n media get catalogs
    NAME      SIZE   COPIES   READY   AGE
    catalog   1Gi    1        True    3d

`Ready` follows the catalog replica pods and the storage configuration.
A replica that cannot run keeps the catalog from becoming ready;
a screen that is down does not. `status.replicas.catalog` and
`status.replicas.progress` report the ready and requested counts for
each store. `status.members` lists the catalog cluster's member pods,
and `status.screens` lists each screen with its claims, node, and phase.

## The catalog pod

`spec.storage.replicas` sets the number of durable catalog replicas
and defaults to one. Their pods are named `<catalog>-catalog-0`,
`<catalog>-catalog-1`, and so on, where `<catalog>` is the resource's
name. Even a single replica has the `-0` suffix.

Every replica runs a `catalog` container with the Corrosion agent and
a `confirmer` container. The agent is a native sidecar in
`spec.initContainers`. The confirmer checks that worker writes reached
this replica, as described below. Replica zero also runs a `reporter`
container, so it has three running containers; other replicas have two.

The reporter reads its local agent and publishes one retained report
per `Library` on the bus. It rebuilds the report whenever the catalog
changes, at most once a second. These reports supply a `Library`'s
counts, gaps, and runs. The reporter has no Kubernetes credential.
The operator alone writes status.

All durable replicas mount the same claim, named `<catalog>-catalog`
unless `spec.storage.claimName` names an existing one. With a per-node
storage class, each node has a separate directory for that claim.
The operator provisions its claims as `ReadWriteMany` on that class
and places replicas on different nodes. It provisions `ReadWriteOnce`
claims on other classes. If more than one replica is requested on a class
whose provisioner is not `per-node.liken.sh`, the operator runs one
replica and reports `Ready: False` with reason `ClassNotPerNode`.

The `Catalog` also creates a separate Corrosion cluster for playback
progress. `spec.progress.replicas` controls its replica count. Progress
has separate storage because a catalog rescan cannot recover it.

The agent's API binds to loopback, so nothing on the pod network can
reach it. The pod's probes run `SELECT 1` through the agent's own
binary inside the container. The startup probe allows ninety seconds
for the agent to open its database.

## How the members find each other

The operator writes a headless `Service` named `catalog` in the
namespace, on UDP port 8787, and writes its `EndpointSlice` itself.
The slice holds every pod in the namespace that has the member
label: the catalog replica pods, every running scan, enrich, and cleanup `Job`,
and every screen. A starting agent is published before it is ready,
because it is a gossip peer as soon as it starts. Every agent
bootstraps to `catalog:8787` and keeps re-resolving it for as long as
it runs.

## How a `Job` confirms its rows landed

Every worker `Job` writes a `runs` row when it starts. It updates that
row when it finishes. The finished write returns the writing agent's id
and the database version of that write. The `Job` then records that
agent id and database version in the row, and waits for confirmation.

Every catalog pod runs a `confirmer` beside its agent. The `confirmer`
follows each finished run. It reads `crsql_db_versions` and
`__corro_bookkeeping_gaps` from its own copy. Those tables show whether
the copy has every version from that agent through the version named by
the run. Once it does, the `confirmer` writes a row to `confirmations`
under its own pod name. The `Job` exits on the first confirmation for
its run and version. The confirmation means that this catalog copy received
every version from that agent through the version recorded by the `Job`.

A receiving agent can apply versions out of order. It records missing
earlier versions as gaps, so the newest version does not prove that
earlier versions arrived. While it waits, the `Job` rewrites its
`runs` row every ten seconds with a later finish time. Each rewrite is a
new broadcast to current peers. A `Job` that waits more than two minutes
fails, and Kubernetes retries it. `HANDOFF_TIMEOUT` sets that limit. The
rows remain safe on the `Job`'s own claim.

## How a screen syncs

A screen pod runs the same agent as a native sidecar, and the browser
starts only after the agent's startup probe passes. The agent's claim
is named `<screen-pod>-catalog`, sized from the `Catalog`, and classed
by `spec.screens.storageClassName`. A screen pod is pinned to the
machine that holds its display, so a node-local class fits.

A screen holds a second claim beside it, `<screen-pod>-art`, where
the browser keeps every piece of art it scaled: posters, backdrops,
episode stills, logos, and headshots. `spec.screens.artCache.size`
sizes it, 2Gi by default, and it takes the same class. The browser
keeps its cache 128 MiB under that size, which is the room an atomic
write needs. Both claims are created once and never updated, so a
size change reaches new screens and not existing ones. To resize an
existing screen, delete its claim, and the next pass creates it at
the new size.

The catalog claim preserves the screen's database across restarts.
With an `emptyDir`, a replacement pod must sync the whole catalog.
With a claim, it reuses the database and receives any missing updates.
The following recorded comparison measured restart time and memory
use. These measurements are not performance guarantees:

| Screen restart | On an `emptyDir` | On a claim |
|---|---|---|
| Time to the full catalog | 157 s | 0 to 1 s |
| Agent memory after | 211 MiB | 11 MiB |
| First start on a fresh claim | every start | once, 152 s |

A screen in a namespace with no `Catalog` runs on an `emptyDir` and
syncs the whole catalog whenever its pod is replaced.

A node-local class binds the claim to the node the pod first landed
on. If the display moves to another machine, the pod cannot schedule
there. After more than five minutes unschedulable, the operator checks
for a bound catalog claim owned by that `Player`. If it finds one on
a class other than per-node, it deletes the pod and catalog claim,
and removes the art claim if that claim is also bound and owned by
the `Player`. The next pass recreates them for the new node. This
recovery does not delete claims on a per-node class, since those
claims do not pin the pod to one node.

## Reading the catalog by hand

The catalog pod's images have no shell. The agent's own binary answers
queries and lists the cluster's members. For a `Catalog` named
`catalog`, query replica zero:

    kubectl -n media exec catalog-catalog-0 -c catalog -- /corrosion query "SELECT COUNT(*) FROM movies"
    kubectl -n media exec catalog-catalog-0 -c catalog -- /corrosion cluster members

The tables are in the repository at `corrosion/schema/catalog.sql`,
with a comment on each. Every table is keyed by its library first, so
two libraries in one namespace never share a row.
