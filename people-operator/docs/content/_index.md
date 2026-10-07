---
title: people-operator
---

# `people-operator`

`people-operator` declares the people of a household on a
[`liken`](https://liken.sh/docs/) cluster, so the screens can ask who
is watching and keep each person's place in what they watch. You declare
each person as a `Person`, with a name, a short name, and a picture.
The operator reads the picture and writes a small thumbnail of it into
the `Person`'s status, and every screen draws that thumbnail.

A `Person` also holds a Linux uid and a link to an outside login. It's
a cluster-scoped resource, and other operators refer to it by name and
keep their own facts about that person:
[`media-operator`](https://liken.sh/media/)'s `Play` names the people
who watched it, and [`library-operator`](https://liken.sh/library/)
keeps each person's progress in each title. This manual covers the
`Person` itself.

Start here:

* [Install](/docs/guides/install/) the operator and declare your
  people.
* [Give a person a picture](/docs/guides/pictures/), and find out why
  a picture didn't show.
* [Reference](/docs/reference/people/): every field of a `Person`.

`people-operator` is one of the extension operators for
[running a home theater](https://liken.sh/docs/concepts/running-a-home-theater/).

* [The source](https://github.com/liken-sh/liken/tree/main/people-operator)
* [The `liken` manual](https://liken.sh/docs/)
