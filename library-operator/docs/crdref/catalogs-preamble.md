A `Catalog` is a namespace's shared catalog: one Corrosion cluster that
every `Library` in the namespace writes into. Declare one `Catalog` in a
namespace. It stands the catalog pod, the one standing member of that
cluster, which holds the namespace's catalog on a durable claim and
reports what it holds over the bus. It sizes that claim and the claim
every `Library`'s `Job`s take, and it owns the pod, its claim, and the
namespace's catalog `Service` and `EndpointSlice`.

Each catalog agent holds the whole namespace's catalog, because the
cluster gossips every row to every peer. So one size covers the whole
namespace, on the `Catalog`, in place of a size on each `Library`.

    apiVersion: library.liken.sh/v1alpha1
    kind: Catalog
    metadata:
      name: catalog
      namespace: media
    spec:
      storage:
        size: 1Gi

A namespace has exactly one `Catalog`. A `Library` in a namespace with no
`Catalog` waits until one exists, and more than one `Catalog` marks every
`Catalog` in the namespace `Blocked` and stands no cluster. An empty
`storageClassName` binds each catalog volume to the cluster's default
`StorageClass`. A `claimName` names an existing claim for the catalog
pod to mount in place of the one the operator provisions. A SQLite
file on a claim served over NFS can corrupt when its node is lost, so
prefer a `StorageClass` that binds node-local storage.
