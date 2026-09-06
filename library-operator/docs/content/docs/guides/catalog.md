---
title: The catalog
weight: 40
---

# The catalog

The catalog is what the scanners write and what every screen reads. It
is a SQLite database replicated by [Corrosion](https://github.com/superfly/corrosion),
one cluster per namespace, with one standing member and a member in
every pod that reads or writes it. This guide describes the `Catalog`
resource that creates it, how a screen gets its copy, and how the
counts reach a `Library`'s status.

## The Catalog resource

One `Catalog` per namespace creates the catalog pod, sizes every
catalog claim in the namespace, and owns the `Service` the cluster's
members find each other through:

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

Every member holds the whole namespace's catalog, because the cluster
gossips every row to every peer. So one `size` covers the catalog
pod, every scan `Job`, and every screen. `spec.storage.claimName`
names an existing claim for the catalog pod in place of the one the
operator provisions. [Catalog](/docs/reference/catalogs/) describes
every field.

The listing shows the size and whether the pod runs:

    $ kubectl -n media get catalogs
    NAME      SIZE   READY   AGE
    catalog   1Gi    True    3d

`Ready` follows the catalog pod alone. A dark screen does not report
the namespace's catalog as down. `status.members` lists the pods in
the cluster, and `status.screens` lists each screen with its claim,
its node, and its phase.

## The catalog pod

The pod is named `<catalog>-catalog`, and it runs two containers. The
`catalog` container is the Corrosion agent, on a `ReadWriteOnce` claim
named the same way, so a restarted pod keeps its database. The
`reporter` container reads that agent and publishes one retained
report per `Library` on the bus, rebuilt whenever the catalog changes,
at most once a second. The report is where a `Library`'s counts, gaps,
and runs come from. The reporter holds no Kubernetes credential. The
operator alone writes status.

The agent's API binds to loopback, so nothing on the pod network can
reach it, and the pod's probes run `SELECT 1` through the agent's own
binary inside the container. The startup probe allows ninety seconds
for the agent to open its database.

## How the members find each other

The operator writes a headless `Service` named `catalog` in the
namespace, on UDP port 8787, and writes its `EndpointSlice` itself.
The slice holds every pod in the namespace that carries the member
label: the catalog pod, every running scan, enrich, and cleanup `Job`,
and every screen. A starting agent is published before it is ready,
because it is a gossip peer as soon as it starts. Every agent
bootstraps to `catalog:8787` and keeps re-resolving it for its whole
life.

## How a `Job` confirms its rows landed

Every worker `Job` writes a `runs` row when it starts and one when it
finishes, then subscribes to its `Library`'s report and waits until the
reporter echoes that run back with the counts the `Job` itself held.
The echo is the proof, because a version can apply before the rows
behind it finish replicating. A `Job` that waits more than two minutes
fails, and Kubernetes retries it. The rows stay safe on the `Job`'s
own claim.

## How a screen syncs

A screen pod runs the same agent as a native sidecar, and the browser
starts only after the agent's startup probe passes. The agent's claim
is named `<screen-pod>-catalog`, sized from the `Catalog`, and classed
by `spec.screens.storageClassName`. A screen pod is pinned to the
machine that holds its display, so a node-local class fits.

The claim is what makes a restart fast. On an `emptyDir`, a restarted
screen synced the whole catalog again every time. On a claim, the
sync happens once, when the claim is fresh:

| Screen restart | On an `emptyDir` | On a claim |
|---|---|---|
| Time to the full catalog | 157 s | 0 to 1 s |
| Agent memory after | 211 MiB | 11 MiB |
| First start on a fresh claim | every start | once, 152 s |

A screen in a namespace with no `Catalog` runs on an `emptyDir` and
pays the sync on every start.

A node-local class binds the claim to the node the pod first landed
on. If the display moves to another machine, the pod cannot schedule
there. After five minutes unschedulable, the operator deletes the pod
and its claim together, and the next pass creates both on the new
node.

## Reading the catalog by hand

Neither image in the catalog pod has a shell. The agent's own binary
answers queries and lists the cluster's members:

    kubectl -n media exec catalog-catalog -c catalog -- /corrosion query "SELECT COUNT(*) FROM movies"
    kubectl -n media exec catalog-catalog -c catalog -- /corrosion cluster members

The tables are in the repository at `corrosion/schema/catalog.sql`,
with a comment on each. Every table is keyed by its library first, so
two libraries in one namespace never share a row.
