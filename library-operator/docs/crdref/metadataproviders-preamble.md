A `MetadataProvider` is one account with one metadata provider: the
`Secret` that holds its key, and the facts it may serve. It lives
in the namespace of the libraries that name it, because a pod mounts
a `Secret` only from its own namespace. A `Library` names providers by
name in `spec.sources`, in the order they are asked, and for each
fact the first provider in that list that serves it is the one
asked. The `trailer` and `marks` facts take the union of their
providers, so they ask every provider in the list that serves them.

A `MetadataProvider` names exactly one provider block: `tmdb`,
`omdb`, `fanart`, `tvmaze`, `peertube`, `archive`, `theintrodb`,
`introdb`, or `imdb`. TMDb, OMDb, and Fanart.tv take a key from a
`Secret`; TVmaze, the Internet Archive, IntroDB, and IMDb take none,
so their blocks are empty. TheIntroDB takes a key where its block names a `Secret`
and asks with none where it names none. PeerTube takes no key either,
but it is software that many people run, so its block names the
address of one instance. The `PROVIDER` column shows the block.

The operator paces every provider: one request at a time per block,
with a fixed gap between requests that fits each provider's stated
limits, and a quarter second for the Internet Archive and IntroDB.

`spec.facts` is optional. A provider that names none serves every
fact the operator can request, and `status.facts`, shown
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
    kind: MetadataProvider
    metadata:
      name: imdb
      namespace: media
    spec:
      imdb: {}
    ---
    apiVersion: library.liken.sh/v1alpha1
    kind: Library
    metadata:
      name: movies
      namespace: media
    spec:
      sources: [tmdb, imdb, omdb, tvmaze]
      # the rest as before

The operator checks each provider with one call to the provider, and
reports the answer in the
`Ready` condition: `Reachable`, `NoSecret`, `Refused`, `Unreachable`, or
`Unavailable`. `Unreachable` is a check that got no answer at all and
carries the error as its message. `Unavailable` is a check the provider
answered with a status that says nothing about the account, and its
message names that status code. The key reaches each phase container
of a `Library`'s `Job` through a `secretKeyRef` that the kubelet
resolves. It never passes through a status, a log, or the
catalog.

The check calls a provider when the operator starts, when the
provider's `metadata.generation` changes, and when its `Secret` changes.
Otherwise it calls a provider whose last answer was `Reachable` or
`Refused` once an hour, and one whose last answer was `Unreachable` or
`Unavailable` every five minutes. A refused key does not repair itself,
so an edit of the `Secret` is what calls again at once. An account with
a daily allowance, such as OMDb's thousand calls, spends at most
twenty-four of them a day on the check.

The `imdb` block has no API to call. IMDb publishes its datasets as
files, so the check sends one `HEAD` request for each file that the
provider's served facts read: `title.ratings` and `title.episode` for
`rating.imdb`, and `title.principals` and `name.basics` for `credits`. Every file must answer `200` for `Reachable`. Any other
status is `Unavailable`, and the message names the file. The check
keeps the same schedule as every other provider, so while IMDb
answers it sends one `HEAD` request for each file an hour. A `HEAD`
request transfers no file.

`status.imdb.datasets` lists what IMDb returned for each file: its
`lastModified`, `etag`, and `size`. A failed check leaves the entries
of the last check that read the files. The `UPDATED` column shows the
oldest `lastModified`. IMDb replaces each file every day, so a file
older than three days writes the `Stale` condition with status `True`,
and its message names the file and its date. `Stale` does not change
`Ready`, because a file from last week still gives correct ratings and
credits.

    $ kubectl -n media get metadataprovider imdb
    NAME   PROVIDER   READY   REASON      UPDATED   AGE
    imdb   imdb       True    Reachable   9h        3d

The operator caches the files on the claim `<provider>-datasets`, 3Gi,
in the provider's namespace. The provider owns the claim, so deleting
the provider deletes the cache. The claim is on the cluster's
`StorageClass` whose provisioner is `per-node.liken.sh`, whatever class
the libraries use, and each node keeps its own copy of each file. The
`Cached` condition is `True` with the reason `PerNodeClass` when the
claim exists. A cluster with no per-node class gets no claim, and
`Cached` is `False` with the reason `NoPerNodeClass`. Each enricher
run then reads the files from IMDb directly. `Cached` does not change
`Ready` either.
