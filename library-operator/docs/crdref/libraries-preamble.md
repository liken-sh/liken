A `Library` is one root directory on one volume, holding media of one
kind. It names a `PersistentVolumeClaim` in its namespace, a directory
inside it, and the kind that directory holds. The operator reconciles
it into a `CronJob` whose `Job`s walk the volume into the namespace's
catalog, and it reports back what the catalog holds: how many titles,
how many folders the walk could not identify, and when it last walked.
An import can rescan one folder at once through the webhook address in
the status.

A namespace may hold many libraries, of any mix of kinds, and one
volume may hold several libraries, each with its own root. The kind
and the storage are immutable: a different volume or a different kind
is a different `Library`.

    apiVersion: library.liken.sh/v1alpha1
    kind: Library
    metadata:
      name: movies
      namespace: media
    spec:
      storage:
        claim: movies
        root: /
      kind: movies
      movies: {}

The block named by `kind` must be present and the other kinds' blocks
must not. Each block holds that kind's own settings, and an empty
block is a complete one.
