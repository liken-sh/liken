# people-operator

A `Person` is a fact of a `liken` cluster: someone the household names
once, so every operator can name them again. This repository ships the
CRD, `people.liken.sh/v1alpha1`, and no controller.

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: thora
spec:
  displayName: Thora
```

The resource is small because its value is in who references it. A
`Watch` in [`library-operator`](https://github.com/liken-sh/library-operator)
names the people who share a series. A `Play` names the people who
watched it, through owner references. A `Player` in
[`media-operator`](https://github.com/liken-sh/media-operator) names
the people who usually sit in front of it. Each reference is a plan in
the operator that makes it.

Two fields wait for a controller. `spec.uid` is the Linux uid a
person's files are owned by, stated by hand today and assigned from a
reserved range later. `spec.identity` names an outside login as an
OIDC issuer and subject, and nothing reads it yet.

The manual is at [people.liken.sh](https://people.liken.sh/).
`plans/00-design.md` is the design, and `plans/README.md` indexes the
plans. `make test` runs every check CI runs.
