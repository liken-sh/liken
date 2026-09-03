A `MetadataProvider` is one account with one metadata provider: the
`Secret` that holds its key, and the facts it may serve. It lives
in the namespace of the libraries that name it, because a pod mounts
a `Secret` only from its own namespace. A `Library` names providers by
name in `spec.sources`, in the order they are asked, and for each
fact the first provider in that list that serves it is the one
asked.

A `MetadataProvider` names exactly one provider block: `tmdb`,
`omdb`, `fanart`, or `tvmaze`. TMDb, OMDb, and Fanart.tv take a key
from a `Secret`; TVmaze takes none, so its block is empty. The
`PROVIDER` column shows the block.

`spec.facts` is optional. A provider that names none serves every
fact the operator knows how to ask it for, and `status.facts`, shown
in the `FACTS` column, lists what it serves right now. That list is
empty while the provider is not `Ready`.

    apiVersion: library.liken.sh/v1alpha1
    kind: MetadataProvider
    metadata:
      name: tmdb
      namespace: media
    spec:
      tmdb:
        secretRef:
          name: tmdb-key
          key: token
      facts: [identity]   # optional; omit to serve the whole table
    ---
    apiVersion: library.liken.sh/v1alpha1
    kind: MetadataProvider
    metadata:
      name: omdb
      namespace: media
    spec:
      omdb:
        secretRef:
          name: omdb-key
    ---
    apiVersion: library.liken.sh/v1alpha1
    kind: MetadataProvider
    metadata:
      name: tvmaze
      namespace: media
    spec:
      tvmaze: {}
    ---
    apiVersion: library.liken.sh/v1alpha1
    kind: Library
    metadata:
      name: movies
      namespace: media
    spec:
      sources: [tmdb, omdb, tvmaze]
      # the rest as before

The operator checks each provider once per pass with one call to the
provider, and reports the answer in the
`Ready` condition: `Reachable`, `NoSecret`, `Refused`, or
`Unreachable`, where the last is a check that got no answer at all and
carries the error as its message. The key
reaches an enricher container through a `secretKeyRef` that the
kubelet resolves. It never passes through a status, a log, or the
catalog.
