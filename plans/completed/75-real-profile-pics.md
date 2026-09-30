# 75, Real profile pictures

Built 2026-09-30. This plan gives every `Person` a picture that every screen
can draw with no work of its own. `people-operator` becomes a process:
it reads each `Person`'s `spec.avatar`, bakes one small thumbnail, and
writes it into the `Person`'s status. The media browser's person
picker is the first screen that draws it.

## The problem

The person picker draws each person as a grey circle with the first
letter of their display name. Two people with the same initial look
the same, and a room of children reads faces before it reads letters.

`Person` already has a `spec.avatar` field, a `claim://<claim>/<path>`
reference, and nothing reads it. A screen that read it directly would
need a volume mount for every claim that holds a picture, and the pod
would restart each time a person moved their picture. The media
browser holds no API credential and no TLS client, so it cannot fetch
a picture from the web either.

## The design

A picture is a derived fact about a `Person`, so it goes in the
status. The person declares a source in `spec.avatar`, and
`people-operator` produces `status.thumbnail` from it. Every consumer
reads that one field and draws it. No consumer fetches, resizes, or
mounts anything.

```
Person.spec.avatar          people-operator            Person.status.thumbnail
https://... or data:...  -> fetch, decode, crop,    -> data:image/jpeg;base64,...
(or nothing)                scale, encode              (always present)
                            (or draw the initials)
```

### The thumbnail

`status.thumbnail` is a `data:` URI that holds a JPEG, 256 by 256
pixels, at quality 85. A square JPEG of a face at that size is about
15 to 25 KB, or 20 to 35 KB after base64. The limit on one object is
etcd's request limit, 1.5 MiB by default, so the thumbnail takes about
2% of it. The 256 pixels cover the picker's 160-pixel circle on a
screen that scales by up to 1.6.

The thumbnail is square and has no alpha. Each consumer crops it to
its own shape: the picker masks it to a circle when it decodes it. A
square keeps the status free of any one screen's design, and JPEG
holds a photograph in a quarter of the bytes that PNG needs.

When `spec.avatar` is empty, or its source fails, the operator draws
the thumbnail itself: the person's initials in white on a colour taken
from a hash of the `Person`'s name. So `status.thumbnail` is present
for every `Person` the operator has seen, and every screen draws the
same face for one person. Initials come from the nickname when one is
set, and from the display name otherwise.

### The sources

`spec.avatar` takes every scheme that a `Play` takes, and `data:`:

- `https://` and `http://`: the operator fetches the URL itself.
- `data:`: the picture is inline in the `Person`, and the operator
  decodes it.
- `nfs://<server>/<path>` and `claim://<namespace>/<claim>/<path>`:
  a baker pod reads the file, as the next section describes.

A claim URI names its namespace first everywhere in `liken`, so one
URI means one file wherever it appears. A `Person` is cluster-scoped
and has no namespace of its own to imply. A `Play` takes the same
form: `media-operator` refuses a `Play` whose claim URI names a
namespace other than the `Play`'s own, because a pod can mount only a
claim in its own namespace. `library-operator` writes the namespace
into every claim URI of a play request. The two operators change the
form in one release, and a cluster upgrades both together.

### The baker pod

The operator reads no NFS export and mounts no claim. Only the kubelet
mounts either one, so for an `nfs://` or a `claim://` source the
operator starts a short-lived pod, the way `media-operator` starts a
playback pod for the same schemes. The pod runs the `people-operator`
image as `people-operator bake <path>`, with the source mounted
read-only: an `nfs` volume of the file's directory, or the claim. It
reads the file, applies the same limits as a fetch, bakes the
thumbnail, and writes one line of JSON to its standard output: the
`data:` URI, the file's modification time, and its size. Then it
exits.

The operator reads that line from the pod's log, writes the status,
and deletes the pod. So the baker pod holds no API credential: it
mounts no service account token, and it cannot write to the API
server. A claim's pod runs in the claim's namespace, because a pod can
mount only a claim in its own namespace. An `nfs://` pod runs in
`people-operator`'s namespace.

A baker pod that does not finish within two minutes is deleted, and
the condition reports `BakeFailed`. That covers an NFS server that
does not answer and a claim that is bound to a node the pod cannot
reach.

### When the operator acts

The operator watches `Person` through client-go's reflector, as every
operator in the repository does. It bakes a thumbnail when:

1. A `Person` has no thumbnail, or its `spec.avatar` differs from the
   source recorded in `status.avatar.source`.
2. The `people.liken.sh/check-avatar` annotation holds a value that
   differs from `status.avatar.checked`. The value has no meaning. A
   change of the value is the request, as with the
   `liken.sh/check-releases` annotation on a `Cluster`.
3. The slow check finds a new picture at an HTTP source.

An HTTP URL has no watch, so a picture replaced at the same URL
changes nothing in the `Person`, and neither does a file replaced on
NFS or on a claim. The slow check covers that case: every six hours
the operator sends a conditional `GET` for each HTTP source, with the
`ETag` and `Last-Modified` it recorded in `status.avatar`. A
`304 Not Modified` costs one small request and changes nothing. For
an `nfs://` or a `claim://` source, the slow check starts a baker pod,
and the operator writes the status only when the modification time
or the size differs from what `status.avatar` records. A
person who wants the new picture now sets the annotation. A comment
at the timer states this reason, as the repository's rules require.

### The status

```yaml
status:
  thumbnail: "data:image/jpeg;base64,/9j/4AAQ..."
  avatar:
    source: https://example.com/people/ada.jpg   # the spec.avatar it came from
    etag: '"5f3a-61c2"'                          # HTTP only
    lastModified: "Tue, 29 Sep 2026 14:02:11 GMT" # HTTP, or the file's time
    size: 48213                                   # nfs:// and claim:// only
    checked: "2026-09-30T19:00:00Z"               # the annotation value it answered
  conditions:
    - type: AvatarReady
      status: "True"
      reason: Fetched       # or Baked, Inline, Initials, FetchFailed,
                            # BakeFailed, DecodeFailed, UnsupportedScheme
      observedGeneration: 3
```

When a source that worked before fails, the operator keeps the last
good thumbnail and sets `AvatarReady` to `False` with the reason. A
failure never replaces a photograph with initials. A `Person` that
never had a good picture shows its initials.

### The limits on a fetch

`spec.avatar` is a URL that the operator fetches from inside the
cluster. Only a person who can write a cluster-scoped `Person` can
set one, which on a `liken` cluster is the owner. The operator still
bounds each fetch:

- a 15-second timeout;
- a 10 MiB limit on the body;
- a check of the image's dimensions before it decodes the pixels, with
  a limit of 8192 pixels on each side, so a small file that declares a
  huge image cannot exhaust the operator's memory;
- JPEG, PNG, GIF, and WebP, the formats Go decodes with the standard
  library and `golang.org/x/image`.

### The consumers

`library-operator` already watches `Person` and writes the
`library-people` `ConfigMap` into each screen namespace. Each entry of
`people.json` gains a `thumbnail` field, copied from the status. The
kubelet rewrites the mounted file when the map changes, so a new
picture reaches the screens with no pod restart. A `ConfigMap` holds
1 MiB, which fits about 30 people at the thumbnail's size.

The media browser reads `thumbnail`, decodes the JPEG, masks it to a
circle, and draws it in the picker's tile in place of the letter. An
entry with no thumbnail, from a cluster where `people-operator` does
not run, keeps the letter.

## The reasons

- **Status and not a sidecar file.** The status travels on the watch
  that every operator already holds. A change to the picture reaches
  every consumer with no new channel.
- **One producer.** `people-operator` owns `Person`, so it owns what
  is derived from `Person`. The design doc already names a controller
  for uids. This controller is the loop that one will join.
- **A `data:` URI and not raw bytes.** A CRD field is a string. A
  `data:` URI carries its media type, so a later change of format does
  not change the field.
- **The initials in the operator.** A screen that drew its own
  fallback would differ from the next screen. The operator draws it
  once, with one font, and every screen shows the same face.

## What could go wrong

- **The operator does not run.** No thumbnails are written, and the
  picker draws letters as it does now. Nothing depends on the
  operator to start.
- **A source is down.** The last good thumbnail stays, and the
  condition says why. The slow check tries again in six hours, and
  the annotation tries again now.
- **The operator is rebuilt.** It keeps no state outside the `Person`
  status, so a new pod reads the status and bakes only what is
  missing or changed.
- **Watch size.** Every consumer that lists `Person` receives the
  thumbnails. At 35 KB a person, a household of ten costs 350 KB for
  each full list, which a watch sends once.

## Not in this plan

- The marks on the continue-watching row, which draw one letter per
  watcher, can draw the thumbnail later.
- Uploads. A person puts the picture somewhere the operator can reach
  over HTTP, or inline.

## How it will be proved

- `people-operator`'s tests bake a thumbnail from an HTTP source, from
  a `data:` source, and from no source, against a fake API server on
  fake time. They prove the conditional `GET`, the annotation, the
  kept thumbnail on a failure, and each limit on a fetch. They prove
  the baker pod's spec for an `nfs://` and a `claim://` source, the
  read of its log line, its deletion, and its two-minute deadline, and
  they run `bake` against a file on disk.
- `library-operator`'s tests prove that `people.json` carries the
  thumbnail.
- The media browser's tests prove that the picker draws the image for
  an entry with a thumbnail and the letter for one without.
- A drill on the testbed: set a `Person`'s `spec.avatar` to an HTTPS
  URL, see the picture in the picker, replace the file at the URL, set
  the annotation, and see the new picture with no pod restart.
