# people-operator

A `Person` names one person who uses a `liken` cluster. This
directory holds the definition of that resource,
`people.liken.sh/v1alpha1`, and nothing more. No program runs.

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

`uid` and `identity` have no consumer yet. `uid` is the Linux uid that
owns a person's files. `identity` is a login at an outside identity
provider, identified by an OIDC issuer and subject. Nothing reads
either field yet.

The manual is at [people.liken.sh](https://liken.sh/people/).
`plans/00-design.md` is the design, and `plans/README.md` indexes the
plans. `make test` runs every check CI runs.
