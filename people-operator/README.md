# people-operator

A `Person` is one person who uses a `liken` cluster. This
directory contains the definition of that resource,
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

Other operators refer to a `Person` by name and keep their own data
about it. A `Play` of [`media-operator`](../media-operator/) lists the
people who watched it as its owner references, and
[`library-operator`](../library-operator/) keeps each person's place
in what they watch.

`spec.avatar` is the URI of a person's picture: an `https://` or
`http://` URL, a `data:` URI, a file on an NFS export (`nfs://`), or a
file on a claim (`claim://`).
The operator reads the picture, crops and scales it to a 256 pixel
square JPEG, and writes it to `status.thumbnail`. A person with no
picture gets their initials. Screens draw `status.thumbnail`, so no
screen has to fetch or mount the picture.

No component reads `uid` or `identity`. `uid` is the Linux uid that
owns a person's files. `identity` is a login at an outside identity
provider, given as an OIDC issuer and subject.

The manual is at [liken.sh/people](https://liken.sh/people/).
`plans/00-design.md` is the design, and the `plans/` directory holds
the plans. `make test` runs the checks that CI runs.
