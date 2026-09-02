---
title: Metadata providers
weight: 30
toc: true
---

<!-- Generated from deploy/metadataproviders-crd.yaml by crdref. Do not edit. -->

A `MetadataProvider` is one account with one metadata provider: the
`Secret` that holds its key, and the concerns it may serve. It lives
in the namespace of the libraries that name it, because a pod mounts
a `Secret` only from its own namespace. A `Library` names providers by
name in `spec.sources`, in the order they are asked, and for each
concern the first provider in that list that serves it is the one
asked.

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
      concerns: [identity]
    ---
    apiVersion: library.liken.sh/v1alpha1
    kind: Library
    metadata:
      name: movies
      namespace: media
    spec:
      sources: [tmdb]
      # the rest as before

The operator checks each provider once per pass with one call to the
provider's configuration endpoint, and reports the answer in the
`Ready` condition: `Reachable`, `NoSecret`, or `Refused`. The key
reaches an enricher container through a `secretKeyRef` that the
kubelet resolves. It never passes through a status, a log, or the
catalog.

One account with one metadata provider, named by the Libraries of its namespace in spec.sources.

## spec

The provider this account is with, and the concerns it may serve.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--tmdb"></span>`tmdb` | [object](#spectmdb) | yes | The account is with The Movie Database, which serves movies, series, and people. |
| <span id="spec--concerns"></span>`concerns` | []string | yes | The concerns this account may serve, from the fixed vocabulary. A Library asks this provider only for a concern it lists. |

### spec.tmdb

The account is with The Movie Database, which serves movies, series, and people.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectmdb--secretref"></span>`secretRef` | [object](#spectmdbsecretref) | yes | The Secret in this namespace that holds the read access token, and the key inside it. |

#### spec.tmdb.secretRef

The Secret in this namespace that holds the read access token, and the key inside it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectmdbsecretref--name"></span>`name` | string | yes | The Secret's name, in this provider's own namespace. |
| <span id="spectmdbsecretref--key"></span>`key` | string | no | The key inside that Secret. When omitted, token. Default: `token`. |

## status

What the operator's own check found, written only by the library operator.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--lastrefusal"></span>`lastRefusal` | string | no | When the provider last refused the key. It stands after the key works again, so a person reads that it once failed. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Ready is True with the reason Reachable when the provider answered the operator's check, and False with the reason NoSecret or Refused. A provider that did not answer at all keeps the verdict it carried. |

### status.conditions[]

Ready is True with the reason Reachable when the provider answered the operator's check, and False with the reason NoSecret or Refused. A provider that did not answer at all keeps the verdict it carried.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The check this entry reports, in CamelCase. It is the key of this list. Pattern: `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The verdict. True is the good verdict, and Unknown means the operator cannot tell yet. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The metadata.generation this condition judged. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One CamelCase word for why the condition holds this verdict, meant for a program to match on. Pattern: `^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`. |
| <span id="statusconditions--message"></span>`message` | string | no | The same answer in a sentence a person reads. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the verdict last changed. It moves only when the status flips. |
