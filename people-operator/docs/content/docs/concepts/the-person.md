---
title: The Person
weight: 10
---

# The `Person`

A `Person` is one person who uses a `liken` cluster. Other operators
need to know who is watching, so that each person keeps their own
place in what they watch. The `Person` gives them one shared name to
refer to.

A `Person` is cluster-scoped, and its name is its identity: every
other resource refers to the person by that name, so choose it once.
The spec holds:

* `displayName`, which a screen shows, and `nickname`, a one-word
  form of it,
* `avatar`, the URI of a picture,
* `uid`, the Linux uid that owns the person's files, for example on a
  NAS, and
* `identity`, the person's login at an outside OIDC identity
  provider.

Nothing assigns or reads `uid` or `identity` yet. They're declared so
that a later controller has a place to find them.

`people-operator` reads the picture that `avatar` names and writes a
256 by 256 thumbnail of it into the `Person`'s status. With no
picture, it draws the person's initials instead. Every screen draws
the thumbnail from the status, so a new picture shows everywhere with
no restart.

Other operators keep their own facts about a person, and refer to the
`Person` by name. [`media-operator`](https://liken.sh/media/)'s `Play`
names the people who watched it, and
[`library-operator`](https://liken.sh/library/) keeps each person's
progress in each title.
