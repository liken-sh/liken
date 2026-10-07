---
title: Give a person a picture
weight: 20
description: "Give a Person a picture with spec.avatar, from an https:// URL, a data: URI, an NFS export, or a claim, and read AvatarReady when a screen shows initials in its place. Use when a person's face should appear on the screens, when a replaced picture does not show, or when a picture fails to read."
---

# Give a person a picture

`people-operator` reads the picture that a `Person`'s `spec.avatar`
names, and writes a square thumbnail of it, 256 by 256 pixels, into
`status.thumbnail`. Every screen draws that one field, so a new
picture reaches every screen with no restart. A `Person` with no
picture gets a thumbnail of their initials, in white on a colour that
the operator takes from the `Person`'s name.

## Name the picture

Set `spec.avatar` to the URI of a JPEG, PNG, GIF, or WebP file:

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: ada
spec:
  displayName: Ada Lovelace
  avatar: https://pictures.example/people/ada.jpg
```

`spec.avatar` takes these schemes:

| Scheme | Who reads it |
| --- | --- |
| `https://<host>/<path>` and `http://<host>/<path>` | The operator fetches the file. |
| `data:image/png;base64,<data>` | The picture is in the field, and the operator decodes it. |
| `nfs://<server>/<path>` | A pod mounts the file's directory from the NFS server, read-only. |
| `claim://<namespace>/<claim>/<path>` | A pod in the claim's namespace mounts the claim, read-only. |

A `Person` has no namespace, so a claim URI names the claim's
namespace first. The pod for an `nfs://` picture runs in
`liken-system`.

The operator crops the centre square of the picture. Put the face in
the middle. A transparent part of the picture shows the person's
colour.

## The limits on a picture

The operator refuses a picture that breaks one of these limits:

- a file larger than 10 MiB;
- a picture wider or taller than 8192 pixels;
- a fetch that takes longer than 15 seconds;
- a pod that does not finish within two minutes.

## Read whether the picture worked

The `Picture` column of `kubectl get people` shows the reason of the
`AvatarReady` condition. `kubectl get people -o wide` adds a `Message`
column with the condition's message. The condition is `True` when the
thumbnail was made from the current `spec.avatar`, and `False` when the
picture did not read. The condition's reason and message say why:

```sh
kubectl get person ada -o jsonpath='{.status.conditions[?(@.type=="AvatarReady")]}'
```

| Reason | Status | Meaning |
| --- | --- | --- |
| `Fetched` | `True` | The operator fetched the picture over HTTP. |
| `Inline` | `True` | The operator decoded the `data:` URI. |
| `Baked` | `True` | A pod read the picture from NFS or a claim. |
| `Initials` | `True` | `spec.avatar` is empty, so the thumbnail shows the initials. |
| `FetchFailed` | `False` | The server did not answer `200`, the file is larger than 10 MiB, or the fetch took longer than 15 seconds. |
| `BakeFailed` | `False` | The pod did not find the file, did not finish in two minutes, or wrote no result. A claim in a namespace that does not exist also fails this way. |
| `DecodeFailed` | `False` | The file is not a JPEG, PNG, GIF, or WebP picture, or it is larger than 8192 pixels on a side. |
| `UnsupportedScheme` | `False` | `spec.avatar` is not a URI in one of the five schemes. |

When a picture that worked before fails, the last good picture stays
in `status.thumbnail`. A `Person` that never had a good picture shows
their initials.

The condition holds only its last change. The operator also posts a
Kubernetes `Event` for each change of the condition's status or
reason, with the same reason and message, so a source that failed and
then worked again is visible for an hour after. A change to `False` is
a `Warning`. A new picture that leaves the condition as it was, such
as a replaced picture at the same URL, posts `AvatarUpdated`. A
`Person` is cluster-scoped, so its `Event`s are in the namespace
`default`:

```sh
kubectl describe person ada
kubectl events -n default --for person/ada
```

A pod that reads a claim runs as root with the `DAC_OVERRIDE`
capability, so it reads a file whatever its owner. An NFS server maps
root to its anonymous user unless the export says otherwise, so a
picture on NFS must be readable by that user.

## Show a replaced picture

A URL, a file on NFS, and a file on a claim can change with no edit to
the `Person`, and the operator gets no event for that change. Every
six hours the operator reads each such picture again: it sends a
conditional `GET` with the `ETag` and `Last-Modified` it recorded, or
it starts a pod that compares the file's time and size. A picture
that did not change costs one small request or one short pod, and
changes nothing.

To show a replaced picture at once, set the
`people.liken.sh/check-avatar` annotation to a new value. The value
has no meaning. A value that differs from `status.avatar.checked` is
the request. A timestamp is a convenient value:

```sh
kubectl annotate person ada --overwrite people.liken.sh/check-avatar="$(date -u +%FT%TZ)"
```

The operator reads the picture again, whether it changed or not, and
records the value in `status.avatar.checked`.
