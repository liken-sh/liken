---
title: Metadata providers
weight: 30
toc: true
---

<!-- Generated from deploy/metadataproviders-crd.yaml by crdref. Do not edit. -->

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

One account with one metadata provider, named by the Libraries of its namespace in spec.sources.

## spec

The provider this account is with, and the facts it may serve. A spec that names no facts serves every fact the operator knows how to ask this provider for.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--tmdb"></span>`tmdb` | [object](#spectmdb) | no | The account is with The Movie Database, which serves movies, series, and people. |
| <span id="spec--omdb"></span>`omdb` | [object](#specomdb) | no | The account is with OMDb. OMDb answers on an IMDb id, and it serves the plot, the US certification, and the ratings of IMDb, Rotten Tomatoes, and Metacritic. |
| <span id="spec--fanart"></span>`fanart` | [object](#specfanart) | no | The account is with Fanart.tv, which serves art alone. It is the only provider of the clearart, the banner, the landscape, the discart, and the season banner. |
| <span id="spec--tvmaze"></span>`tvmaze` | object | no | The account is with TVmaze, which serves series alone and needs no account. The block is empty, and its presence says that the operator may ask TVmaze. |
| <span id="spec--facts"></span>`facts` | []string | no | The facts this account may serve, from the fixed vocabulary. The list narrows what the operator knows how to ask this provider for. Omit it to serve all of that. A Library asks this provider only for a fact that status.facts lists. |

### spec.tmdb

The account is with The Movie Database, which serves movies, series, and people.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectmdb--secretref"></span>`secretRef` | [object](#spectmdbsecretref) | yes | The Secret in this namespace that holds the credential, and the key inside it. Either credential TMDb issues works: a v3 API key of 32 hex characters, or a v4 read access token. |

#### spec.tmdb.secretRef

The Secret in this namespace that holds the credential, and the key inside it. Either credential TMDb issues works: a v3 API key of 32 hex characters, or a v4 read access token.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectmdbsecretref--name"></span>`name` | string | yes | The Secret's name, in this provider's own namespace. |
| <span id="spectmdbsecretref--key"></span>`key` | string | no | The key inside that Secret. When omitted, token. Default: `token`. |

### spec.omdb

The account is with OMDb. OMDb answers on an IMDb id, and it serves the plot, the US certification, and the ratings of IMDb, Rotten Tomatoes, and Metacritic.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specomdb--secretref"></span>`secretRef` | [object](#specomdbsecretref) | yes | The Secret in this namespace that holds the OMDb key, and the key inside it. The free tier of a key is a thousand calls a day. |

#### spec.omdb.secretRef

The Secret in this namespace that holds the OMDb key, and the key inside it. The free tier of a key is a thousand calls a day.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specomdbsecretref--name"></span>`name` | string | yes | The Secret's name, in this provider's own namespace. |
| <span id="specomdbsecretref--key"></span>`key` | string | no | The key inside that Secret. When omitted, token. Default: `token`. |

### spec.fanart

The account is with Fanart.tv, which serves art alone. It is the only provider of the clearart, the banner, the landscape, the discart, and the season banner.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specfanart--secretref"></span>`secretRef` | [object](#specfanartsecretref) | yes | The Secret in this namespace that holds the Fanart.tv project key, and the key inside it. |

#### spec.fanart.secretRef

The Secret in this namespace that holds the Fanart.tv project key, and the key inside it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specfanartsecretref--name"></span>`name` | string | yes | The Secret's name, in this provider's own namespace. |
| <span id="specfanartsecretref--key"></span>`key` | string | no | The key inside that Secret. When omitted, token. Default: `token`. |

## status

What the operator's own check found, written only by the library operator.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--provider"></span>`provider` | string | no | The provider block this account names. The PROVIDER column reads it here, because no printer column can read which block a spec holds. |
| <span id="status--facts"></span>`facts` | []string | no | The facts this provider serves right now: what the operator knows how to ask this provider for, narrowed by spec.facts. The list is empty while the Ready condition is not True, because a provider the operator cannot reach serves nothing. |
| <span id="status--lastrefusal"></span>`lastRefusal` | string | no | When the provider last refused the key. It stands after the key works again, so a person reads that it once failed. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Ready is True with the reason Reachable when the provider answered the operator's check, and False with the reason NoSecret, Refused, or Unreachable. Unreachable is a check that got no answer at all, and its message is the error the check read. |

### status.conditions[]

Ready is True with the reason Reachable when the provider answered the operator's check, and False with the reason NoSecret, Refused, or Unreachable. Unreachable is a check that got no answer at all, and its message is the error the check read.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The check this entry reports, in CamelCase. It is the key of this list. Pattern: `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The verdict. True is the good verdict, and Unknown means the operator cannot tell yet. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The metadata.generation this condition judged. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One CamelCase word for why the condition holds this verdict, meant for a program to match on. Pattern: `^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`. |
| <span id="statusconditions--message"></span>`message` | string | no | The same answer in a sentence a person reads. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the verdict last changed. It moves only when the status flips. |
