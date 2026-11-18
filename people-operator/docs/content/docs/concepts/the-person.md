---
title: The Person
weight: 10
---

# The `Person`

A `Person` is one person who uses a `liken` cluster. Operators that
play or show things need to know who is watching, so that each person
keeps their own place in what they watch. Those operators refer to a
person by the `Person`'s name.

A `Person` is cluster-scoped, and other resources refer to the person
by the object's name, so choose the name carefully. The spec has these
fields:

* `displayName`, which a screen shows, and `nickname`, a short name
  for a screen with little room.
* `avatar`, the URI of a picture.
* `uid`, the Linux uid that owns the person's files, for example on a
  NAS.
* `identity`, the person's login at an outside OIDC identity
  provider.

No component reads `uid` or `identity`.

`people-operator` reads the picture that `avatar` points to and writes
a 256 by 256 pixel thumbnail of it to the `Person`'s status. When
there is no picture, it draws the person's initials. Screens draw the
thumbnail from the status, so a new picture appears everywhere without
a restart.

Other operators keep their own data about a person and refer to the
`Person` by name. A `Play` of [`media-operator`](https://liken.sh/media/)
lists the people who watched it as its owner references, and
[`library-operator`](https://liken.sh/library/) keeps each person's
progress in each title.
