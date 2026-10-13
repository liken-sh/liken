---
title: astrophotography-operator
---

# `astrophotography-operator`

`astrophotography-operator` will image the objects you choose, on a
telescope that [`observatory-operator`](https://liken.sh/observatory/)
runs. You declare a `Target`: an object, its coordinates, and the
frames you want of it, such as sixty five-minute frames of M42
through each of three filters. When you reserve a telescope for the
operator, it will slew to the target, center it with a plate solve,
guide with PHD2, and take the frames. It will move each frame to the
archive you chose, name it the way you asked, and record it in a
catalog beside the frames. A target can take many nights, and its
status keeps the count.

The operator is under development, and it does not image yet. This
version installs the `Target` and `Solver` resources, so the API
server checks every field you write, and it runs the operator, which
elects its leader and watches the resources and does nothing else.
The design is in the
[plans](https://github.com/liken-sh/liken/tree/main/astrophotography-operator/plans),
and each piece joins this manual as it is built.

Start here:

* [Install the operator](/docs/guides/install/) beside
  `observatory-operator`.
* [Targets](/docs/concepts/targets/) explains what a target states
  and how its frames will be named and counted.
* [Solvers](/docs/concepts/solvers/) explains how a frame will be
  plate-solved, and why the index files stay in one place.

`astrophotography-operator` is one of the extension operators for
[running an observatory](https://liken.sh/docs/concepts/running-an-observatory/).
It drives the telescopes that `observatory-operator` runs, and
`observatory-operator` runs with or without it.

* [The source](https://github.com/liken-sh/liken/tree/main/astrophotography-operator)
* [The design documents](https://github.com/liken-sh/liken/tree/main/astrophotography-operator/plans)
* [The `liken` manual](https://liken.sh/docs/)
