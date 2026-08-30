A `Catalog` is a namespace's shared catalog: one Corrosion cluster that
every `Library` in the namespace writes into. Declare one `Catalog` in a
namespace. It sizes the durable volume every catalog agent takes, and it
owns the namespace's catalog `Service` and `EndpointSlice`.

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
`StorageClass`.
