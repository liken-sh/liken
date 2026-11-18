---
title: people-operator
---

# `people-operator`

`people-operator` lets you declare the people of a household on a
[`liken`](https://liken.sh/docs/) cluster, so that operators can
record who is watching and keep each person's place in what they
watch. You declare each person as a `Person` with a name, a short
name, and a picture. The operator reads the picture and writes a small
thumbnail of it to the `Person`'s status, and screens draw that
thumbnail.

A `Person` can also record a Linux uid and a login at an outside
identity provider. It is cluster-scoped, and other operators refer to
it by name and keep their own data about that person: a `Play` of
[`media-operator`](https://liken.sh/media/) lists the people who
watched it, and [`library-operator`](https://liken.sh/library/)
keeps each person's progress in each title. This manual covers the
`Person` itself.

Start here:

* [Install](/docs/guides/install/) the operator and declare your
  people.
* [Give a person a picture](/docs/guides/pictures/), and find out why
  a picture does not show.
* [Reference](/docs/reference/people/): every field of a `Person`.

`people-operator` is one of the extension operators for
[running a home theater](https://liken.sh/docs/concepts/running-a-home-theater/).

* [The source](https://github.com/liken-sh/liken/tree/main/people-operator)
* [The `liken` manual](https://liken.sh/docs/)
