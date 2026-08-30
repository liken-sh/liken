# The namespace catalog

Plan 15. A `Catalog` object, one per namespace, that owns the shared
catalog: where it is stored, how large each agent's copy is, and any
setting the whole catalog needs. At the end of this plan a namespace
declares one `Catalog`, every `Library` in the namespace writes into it,
and each catalog agent holds a durable copy sized by the `Catalog`, not
by any one `Library`.

## The problem

The catalog is a namespace concern, but the design so far treats it as a
`Library` concern. One Corrosion cluster serves a namespace, and every
agent in it holds the whole namespace's catalog, because Corrosion
gossips every row to every peer. So the storage a scanner agent needs is
sized by the whole namespace, not by its own `Library`. A size on the
`Library` would be the same namespace-wide number copied onto every
`Library`, grown on all of them at once. And the catalog `Service` and
`EndpointSlice` that plan 03 hung on the `Library` objects describe the
namespace's one cluster, not any single `Library`.

The catalog also has no durable home yet. Each agent holds the catalog in
an `emptyDir`, so a restart rebuilds it: an agent with peers up re-syncs
the whole catalog over gossip, which is the memory peak the
[`ingest-memory-and-restart`](open-problems/ingest-memory-and-restart.md)
problem records, and an agent with no peers up rebuilds from the volume.
Neither is work the design should repeat on every restart.

## The `Catalog` object

`Catalog` is a namespaced CRD. A namespace has exactly one. Its `spec`
holds the storage every catalog agent in the namespace uses, and it has
room for catalog-wide settings the design grows into.

- `spec.storage.size` is the size of each agent's catalog volume. It is
  one namespace-wide number, because each agent holds the whole
  namespace's catalog. It defaults to a small value, because a catalog
  of movies and series is megabytes and a catalog of a photo library in
  the millions is low gigabytes.
- `spec.storage.storageClassName` is optional. When it is empty, the
  operator omits the class from the claim and the cluster's default
  `StorageClass` binds it. A person sets it to pick a specific class.

The `Catalog` owns the namespace's catalog `Service` and `EndpointSlice`.
They move off the `Library` objects and onto the `Catalog`, which is
their real owner: they describe the namespace's one Corrosion cluster.

## One per namespace

The operator uses the single `Catalog` in a namespace. When a namespace
has more than one, the operator marks every `Catalog` in it `Blocked`,
with a condition that names the conflict, and stands no catalog cluster
until one remains. One cluster per namespace is the rule a second
`Catalog` would break, so the operator refuses rather than build two.

A `Library` in a namespace with no `Catalog` is `Pending`, with a
condition that says the namespace has no `Catalog`. A `Library` depends
on the catalog cluster, and the cluster is the `Catalog`'s to stand, so a
`Library` waits for one. A person declares a `Catalog` and a `Library`
together, which is two objects for the first `Library` and none extra for
the rest.

## The durable volume

The operator provisions one catalog `PersistentVolumeClaim` per scanner
pod, sized from the namespace `Catalog`. The claim is `ReadWriteOnce`,
because one agent writes one SQLite database and Corrosion agents gossip
rather than share a file. The claim is owned by the `Library`, not by the
pod, so it survives a pod roll and holds the catalog the next pod starts
from, and it is garbage-collected when the `Library` is deleted.

The operator stands the scanner pod itself and rolls it by a template
hash. Because the claim is `ReadWriteOnce`, the operator deletes the old
pod and waits for it to release the claim before it creates the new pod.
A new pod that mounted the claim while the old pod still held it would
fail to attach.

A screen's agent stays an `emptyDir` in this plan. Whether a screen also
takes a durable volume from the `Catalog`, to answer the ingest memory
peak, is the screen plan's ground and the ingest-memory problem's.

## The `Catalog` status

`Catalog.status` reports the cluster the `Catalog` stands: the agent
pods that are members, and the storage the agents were given. A person
reads one object to see the namespace's catalog, rather than reading
every `Library` to piece it together.

## What this changes in the other plans

Plan 03 stood the catalog `Service` and `EndpointSlice` and hung them on
the `Library` objects. This plan moves that ownership to the `Catalog`.
Plan 04's scanner reconciliation does not change: it reconciles against
the catalog whatever medium holds it, so the durable volume is
transparent to it.

## Proof

On `liken-1`: a namespace with one `Catalog` and two `Library` objects
stands one catalog cluster, and both `Library` objects reach `Ready`. A
namespace with a `Library` and no `Catalog` holds the `Library`
`Pending` with the no-catalog reason, and the `Library` reaches `Ready`
within seconds of a `Catalog` being declared. A second `Catalog` in a
namespace marks both `Catalog` objects `Blocked`; deleting one clears it.
A scanner pod rolled by a spec change starts on the catalog its claim
already holds, with no full re-sync and no volume rebuild, which the
agent's memory at startup shows against a cold start. Deleting a
`Library` garbage-collects its catalog claim. The `Catalog` status lists
the member pods and the storage size.
