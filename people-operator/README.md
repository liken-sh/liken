# people-operator

A `Person` names one person who uses a `liken` cluster. This
directory holds the definition of that resource,
`people.liken.sh/v1alpha1`, and `people-operator`, which writes each
`Person`'s picture into its status.

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: alex
spec:
  displayName: Alex
  nickname: Alex
```

Other operators refer to a `Person` by name and attach their own
facts to it. A `Play` in [`media-operator`](../media-operator/) names
the people who watched it, through owner references, and
[`library-operator`](../library-operator/) keeps each person's place
in what they watch.

`spec.avatar` names a person's picture: an `https://` or `http://`
URL, a `data:` URI, an `nfs://` file, or a `claim://` file. The
operator reads it, crops and scales it to a 256-pixel square JPEG, and
writes it to `status.thumbnail`. A person with no picture gets their
initials. Every screen draws `status.thumbnail`, so no screen fetches
or mounts anything.

`uid` and `identity` have no consumer yet. `uid` is the Linux uid that
owns a person's files. `identity` is a login at an outside identity
provider, identified by an OIDC issuer and subject. Nothing reads
either field yet.

The manual is at [people.liken.sh](https://liken.sh/people/).
`plans/00-design.md` is the design, and the `plans/` directory holds
the plans. `make test` runs every check CI runs.
