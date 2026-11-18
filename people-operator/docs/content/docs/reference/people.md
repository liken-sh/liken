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
| <span id="spec--displayname"></span>`displayName` | string | yes | The name that a screen shows for this person. |
| <span id="spec--nickname"></span>`nickname` | string | no | A short name for a screen with little room, such as a first name or what people call this person. A screen shows `displayName` when this is not set. |
| <span id="spec--avatar"></span>`avatar` | string | no | The URI of this person's picture, as a JPEG, PNG, GIF, or WebP file. `people-operator` downloads a picture from an `https://` or `http://` URL. A `data:` URI contains the picture itself. `nfs://<server>/<path>` is a file on an NFS export, and `claim://<namespace>/<claim>/<path>` is a file on a claim. For these two, `people-operator` starts a pod that mounts the export or the claim, reads the file, and exits. The file can be up to 10 MiB, and the image up to 8192 pixels on each side. When this is not set, the thumbnail shows this person's initials. |
| <span id="spec--uid"></span>`uid` | integer | no | The Linux uid that owns this person's files. Set it to match files that already exist under a uid, for example on a NAS. No component reads this field. |
| <span id="spec--identity"></span>`identity` | [object](#specidentity) | no | This person's account at an OIDC identity provider, as the issuer URL and the subject. No component reads this field. |

### spec.identity

This person's account at an OIDC identity provider, as the issuer URL and the subject. No component reads this field.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidentity--issuer"></span>`issuer` | string | yes | The issuer URL, written as it is in the provider's discovery document. |
| <span id="specidentity--subject"></span>`subject` | string | yes | The `sub` claim that the issuer assigns to this person. |

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--uid"></span>`uid` | integer | no | The uid that is assigned to this person. No component writes this field. |
| <span id="status--thumbnail"></span>`thumbnail` | string | no | A 256 by 256 pixel JPEG in a `data:image/jpeg;base64,` URI. It is the center square of the picture at `spec.avatar`, or this person's initials when `spec.avatar` is not set or cannot be read. A screen crops it to its own shape. |
| <span id="status--avatar"></span>`avatar` | [object](#statusavatar) | no | The picture that the thumbnail was made from: the value of `spec.avatar`, the version of the picture that `people-operator` read, and the value of the `people.liken.sh/check-avatar` annotation at that time. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `AvatarReady` is `True` when the thumbnail was made from the current `spec.avatar`. Its reason is `Fetched` for an `http://` or `https://` URL, `Baked` for an `nfs://` or `claim://` file, `Inline` for a `data:` URI, and `Initials` when `spec.avatar` is not set. It is `False` when `people-operator` could not read the picture, with the reason `FetchFailed`, `BakeFailed`, `DecodeFailed`, or `UnsupportedScheme`. The thumbnail then keeps the last picture that was read, or shows the initials if there was none. |

### status.avatar

The picture that the thumbnail was made from: the value of `spec.avatar`, the version of the picture that `people-operator` read, and the value of the `people.liken.sh/check-avatar` annotation at that time.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusavatar--source"></span>`source` | string | no | The value of `spec.avatar` that the thumbnail was made from. |
| <span id="statusavatar--etag"></span>`etag` | string | no | The `ETag` header that the HTTP server returned with the picture. `people-operator` sends it as `If-None-Match` when it checks the URL again every six hours. |
| <span id="statusavatar--lastmodified"></span>`lastModified` | string | no | The `Last-Modified` header that the HTTP server returned with the picture, or the modification time of a file on NFS or a claim. |
| <span id="statusavatar--size"></span>`size` | integer | no | The size in bytes of the file on NFS or on a claim. |
| <span id="statusavatar--checked"></span>`checked` | string | no | The value of the `people.liken.sh/check-avatar` annotation when `people-operator` made the thumbnail. When the annotation gets a different value, `people-operator` reads the picture again. |

### status.conditions[]

`AvatarReady` is `True` when the thumbnail was made from the current `spec.avatar`. Its reason is `Fetched` for an `http://` or `https://` URL, `Baked` for an `nfs://` or `claim://` file, `Inline` for a `data:` URI, and `Initials` when `spec.avatar` is not set. It is `False` when `people-operator` could not read the picture, with the reason `FetchFailed`, `BakeFailed`, `DecodeFailed`, or `UnsupportedScheme`. The thumbnail then keeps the last picture that was read, or shows the initials if there was none.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition is true. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | A one-word reason for the status. |
| <span id="statusconditions--message"></span>`message` | string | no | The reason for the status, in a sentence for a person to read. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the Person that the condition was set for. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the status last changed. |
