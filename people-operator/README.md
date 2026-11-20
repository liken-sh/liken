# people-operator

`people-operator` lets you declare the people who use a `liken`
cluster, so that other operators can record facts about each of them,
such as who watched a film and how far each person got in a series.

You declare each person as a `Person`, with a display name, a
nickname, and a picture. The operator reads the picture, crops and
scales it to a square JPEG of 256 by 256 pixels, and writes it to
`status.thumbnail`. A person with no picture gets a thumbnail of
their initials. Screens draw `status.thumbnail`, so no screen fetches
or mounts the picture itself. The operator writes only the status of
each `Person`. Other operators store their own data about a person in
their own resources, and `people-operator` reads none of it. A
`Person` can also record a Linux uid in `spec.uid` and a login at an
outside OIDC identity provider in `spec.identity`. No component reads
either field. The operator doesn't issue logins or assign uids.

The operator depends on no other component, and on nothing that only
`liken` provides. A `Play` of [`media-operator`](../media-operator/)
lists the people who watched it as its owner references.
[`library-operator`](../library-operator/) records how far each person
got in what they watch, and deletes that record when the `Person` is
deleted.

## The kind

- A `Person` is one person, cluster-scoped. `spec.avatar` is the URI
  of their picture: an `https://` or `http://` URL, a `data:` URI, a
  file on an NFS export (`nfs://`), or a file on a claim
  (`claim://<namespace>/<claim>/<path>`). The condition `AvatarReady`
  says whether the thumbnail was made from the current `spec.avatar`.

## A person

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: alex
spec:
  displayName: Alex Example
  nickname: Alex
  avatar: https://pictures.example.com/alex.jpg
```

The manual is at [liken.sh/people](https://liken.sh/people/), and
[`plans/00-design.md`](plans/00-design.md) is the design.

MIT. See [LICENSE](LICENSE).
