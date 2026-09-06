# people-operator

A `Person` names one person who uses a `liken` cluster. This
repository is the definition of that resource, `people.liken.sh/v1alpha1`,
and nothing more. No program runs.

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: thora
spec:
  displayName: Thora
  nickname: Thora
```

Other operators refer to a `Person` by name and attach their own
facts to it. The `Watch` in
[`library-operator`](https://github.com/liken-sh/library-operator)
names the people who watch a series together, and a `Play` names the
people who watched it, through owner references.

Two fields wait for a controller. `uid` is the Linux uid that owns a
person's files. `identity` is a login at an outside identity provider,
as an OIDC issuer and subject. Nothing reads either yet.

The manual is at [people.liken.sh](https://people.liken.sh/).
`plans/00-design.md` is the design, and `plans/README.md` indexes the
plans. `make test` runs every check CI runs.
