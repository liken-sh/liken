---
name: pictures
description: "Give a Person a picture with spec.avatar, from an https:// URL, a data: URI, an NFS export, or a claim, and read the AvatarReady condition to see why a screen shows initials instead. Use when a person's face should appear on the screens, when a replaced picture does not show, or when a picture fails to load."
---

This skill is the guide at https://liken.sh/people/docs/guides/pictures/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Give a person a picture

`people-operator` reads the picture that a `Person`'s `spec.avatar`
points to and writes a 256 by 256 pixel square thumbnail of it to
`status.thumbnail`. Every screen draws that field, so a new picture
appears on every screen without a restart. A `Person` with no picture
gets a thumbnail of their initials in white, on a background color
that the operator picks from the `Person`'s name.

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

`spec.avatar` accepts these schemes:

| Scheme | How the picture is read |
| --- | --- |
| `https://<host>/<path>` and `http://<host>/<path>` | The operator fetches the file. |
| `data:image/png;base64,<data>` | The picture is in the field, and the operator decodes it. |
| `nfs://<server>/<path>` | A pod mounts the file's directory from the NFS server, read-only. |
| `claim://<namespace>/<claim>/<path>` | A pod in the claim's namespace mounts the claim, read-only. |

A `Person` has no namespace, so a `claim://` URI starts with the
claim's namespace. The pod for an `nfs://` picture runs in the
operator's namespace, `liken-system`.

The pod that reads a claim runs as root with the `DAC_OVERRIDE`
capability, so it can read the file whoever owns it. An NFS server
maps root to its anonymous user unless the export says otherwise, so
a picture on NFS must be readable by that user.

The operator crops the largest centered square from the picture, so
keep the face near the middle. Transparent parts of the picture show
the person's background color.

## Limits on a picture

The operator rejects a picture in these cases:

- the file is larger than 10 MiB,
- the picture is wider or taller than 8192 pixels,
- the HTTP fetch takes longer than 15 seconds, or
- the pod that reads an NFS or claim picture does not finish within
  two minutes.

## Check whether the picture loaded

The `Picture` column of `kubectl get people` shows the reason of the
`AvatarReady` condition. `kubectl get people -o wide` adds a `Message`
column with the condition's message. The condition is `True` when the
thumbnail was made from the current `spec.avatar`, and `False` when
the picture could not be read. To read the whole condition:

```sh
kubectl get person ada -o jsonpath='{.status.conditions[?(@.type=="AvatarReady")]}'
```

| Reason | Status | Meaning |
| --- | --- | --- |
| `Fetched` | `True` | The operator fetched the picture over HTTP. |
| `Inline` | `True` | The operator decoded the `data:` URI. |
| `Baked` | `True` | A pod read the picture from NFS or a claim. |
| `Initials` | `True` | `spec.avatar` is empty, so the thumbnail shows the initials. |
| `FetchFailed` | `False` | The server did not respond with `200`, the file is larger than 10 MiB, or the fetch took longer than 15 seconds. |
| `BakeFailed` | `False` | The pod did not find the file, the file is larger than 10 MiB, the pod did not finish in two minutes, or it wrote no result. A claim in a namespace that does not exist also fails this way. |
| `DecodeFailed` | `False` | The file is not a JPEG, PNG, GIF, or WebP picture, or it is wider or taller than 8192 pixels. |
| `UnsupportedScheme` | `False` | `spec.avatar` does not use one of the schemes in the table above. |

When a picture that loaded before fails, `status.thumbnail` keeps the
last good picture. A `Person` that never had a good picture shows
their initials.

The condition shows only the current state. For what happened in the
last hour, read the `Event`s. Each time the condition's status or
reason changes, the operator posts an `Event` with the same reason and
message, and a change to `False` is a `Warning`. When a new picture
loads and the condition stays the same, for example after you replace
the file at the same URL, the operator posts an `Event` with the
reason `AvatarUpdated`. A `Person` is cluster-scoped, so its `Event`s
are in the `default` namespace:

```sh
kubectl describe person ada
kubectl events -n default --for person/ada
```

The kubelet can end a baker pod before the pod writes its result, for
example when it evicts the pod from a node that is low on memory, or
when the node shuts down. That says nothing about the picture, so the
operator writes no `BakeFailed`. It creates a next pod, deletes the
ended one, and posts a `Warning` with the reason `PodRecreated` and the
kubelet's reason in the message. `AvatarReady` keeps its earlier value
until the next pod writes its result.

A pod that ends within 10 seconds of its creation stays until those 10
seconds pass, so you can read its status with `kubectl describe pod`.
When a claim on a node-local storage class keeps the pod on one node,
the kubelet of that node can refuse each next pod. Each refused pod
doubles the wait before the next one, to a limit of 5 minutes. The
first pod that the kubelet ends posts `PodRecreated`. After that, the
operator posts no further `PodRecreated` until a pod runs for longer
than 5 minutes before the kubelet ends it. When you change
`spec.avatar` or the `people.liken.sh/check-avatar` annotation, the
operator deletes the ended pod at once and starts the new bake with no
wait. The annotation
`people.liken.sh/tries` on a pod counts the pods before it that the
kubelet ended. Each next pod restarts the two minutes in which a pod
must finish, so a node that refuses every pod never gives the `Person`
a `BakeFailed`. The `Person` keeps its last picture, or its initials,
for as long as the node refuses the pod.

## Show a replaced picture

When you replace the file behind a URL, an NFS path, or a claim path,
the `Person` does not change, so nothing tells the operator. It reads
each of those pictures again every six hours. For a URL it sends a
conditional `GET` with the `ETag` and `Last-Modified` values from the
last fetch. For NFS and claims it starts a pod that reads the file
again, and the operator compares the file's modification time and
size with those of the last read. When the picture has not changed,
the thumbnail stays as it is.

To show a replaced picture now, set the `people.liken.sh/check-avatar`
annotation to any value that differs from `status.avatar.checked`,
such as a timestamp:

```sh
kubectl annotate person ada --overwrite people.liken.sh/check-avatar="$(date -u +%FT%TZ)"
```

The operator reads the picture again whether it changed or not, and
copies the annotation's value to `status.avatar.checked`.
