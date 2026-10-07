---
title: Person
weight: 10
toc: true
---

<!-- Generated from deploy/people-crd.yaml by crdref. Do not edit. -->

# `Person`

## spec

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--displayname"></span>`displayName` | string | yes | The name a screen displays for this person. |
| <span id="spec--nickname"></span>`nickname` | string | no | A short, one-word name for a screen with little room: a first name, or what people call this person. Optional. A screen displays the display name when this is unset. |
| <span id="spec--avatar"></span>`avatar` | string | no | The URI of this person's picture, in JPEG, PNG, GIF, or WebP. `https://` and `http://` name a file that `people-operator` fetches. `data:` holds the picture in this field. `nfs://<server>/<path>` names a file on an NFS export, and `claim://<namespace>/<claim>/<path>` names a file on a claim; a short-lived pod reads either one. The file is at most 10 MiB and 8192 pixels on each side. Optional. With no picture, the thumbnail shows this person's initials. |
| <span id="spec--uid"></span>`uid` | integer | no | The Linux uid that owns this person's files. Set it when this person's files already exist under a uid, on a NAS for example. Optional. Nothing assigns one yet. |
| <span id="spec--identity"></span>`identity` | [object](#specidentity) | no | This person's login at an outside identity provider, as an OIDC issuer URL and subject. Optional. Nothing reads it yet. |

### spec.identity

This person's login at an outside identity provider, as an OIDC issuer URL and subject. Optional. Nothing reads it yet.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidentity--issuer"></span>`issuer` | string | yes | The issuer URL, exactly as the provider's discovery document states it. |
| <span id="specidentity--subject"></span>`subject` | string | yes | The subject claim, `sub`, that the issuer assigns to this person. |

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--uid"></span>`uid` | integer | no | The uid in effect for this person. Nothing writes it yet. |
| <span id="status--thumbnail"></span>`thumbnail` | string | no | This person's picture as a square JPEG, 256 by 256 pixels, in a `data:image/jpeg;base64,` URI. It is the picture that `spec.avatar` names, cropped to its centre, or this person's initials when there is no picture. A screen crops it to its own shape. |
| <span id="status--avatar"></span>`avatar` | [object](#statusavatar) | no | What the thumbnail was made from: the source, the version of the picture that the operator read there, and the check request. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `AvatarReady` is `True` when the thumbnail was made from the current `spec.avatar`, with the reason `Fetched`, `Baked`, `Inline`, or `Initials`. It is `False` when the picture did not read, with the reason `FetchFailed`, `BakeFailed`, `DecodeFailed`, or `UnsupportedScheme`; the last good picture then stays, or the initials show when there was none. |

### status.avatar

What the thumbnail was made from: the source, the version of the picture that the operator read there, and the check request.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusavatar--source"></span>`source` | string | no | The `spec.avatar` that the thumbnail was made from. |
| <span id="statusavatar--etag"></span>`etag` | string | no | The `ETag` of the picture an HTTP source answered. The check every six hours sends it as `If-None-Match`. |
| <span id="statusavatar--lastmodified"></span>`lastModified` | string | no | The `Last-Modified` of the picture an HTTP source answered, or the modification time of a file on NFS or a claim. |
| <span id="statusavatar--size"></span>`size` | integer | no | The size in bytes of a file on NFS or a claim. |
| <span id="statusavatar--checked"></span>`checked` | string | no | The value of the `people.liken.sh/check-avatar` annotation when the operator made the thumbnail. A new value of the annotation reads the picture again. |

### status.conditions[]

`AvatarReady` is `True` when the thumbnail was made from the current `spec.avatar`, with the reason `Fetched`, `Baked`, `Inline`, or `Initials`. It is `False` when the picture did not read, with the reason `FetchFailed`, `BakeFailed`, `DecodeFailed`, or `UnsupportedScheme`; the last good picture then stays, or the initials show when there was none.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition holds. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | One word for the cause. |
| <span id="statusconditions--message"></span>`message` | string | no | The cause, for a person to read. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The generation of the spec that the condition answers. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the status last changed. |
