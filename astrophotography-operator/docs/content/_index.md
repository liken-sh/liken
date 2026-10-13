---
title: astrophotography-operator
---

# `astrophotography-operator`

`astrophotography-operator` images the objects you choose, on a
telescope that [`observatory-operator`](https://liken.sh/observatory/)
runs. You declare a `Target`: an object, its coordinates, and the
frames you want of it, such as sixty five-minute frames of M42
through each of three filters. When you reserve a telescope for the
operator, it slews to the target and takes the frames. It moves each
frame to the archive you chose, names it the way you asked, and
records it in a catalog beside the frames. A target can take many
nights, and its status keeps the count.

The operator is under development. This version slews and exposes
unguided: it does not center a target with a plate solve and does not
guide with PHD2 yet. It runs the pod of each `Solver` and a worker
beside each reservation's outbox, which together plate-solve a frame,
and nothing sends them a frame yet.
The design is in the
[plans](https://github.com/liken-sh/liken/tree/main/astrophotography-operator/plans),
and each piece joins this manual as it is built.

Start here:

* [Install the operator](/docs/guides/install/) beside
  `observatory-operator`.
* [Targets](/docs/concepts/targets/) explains what a target states.
* [Image a target](/docs/guides/image-a-target/) reserves a telescope
  for the operator and follows the night.
* [The archive](/docs/concepts/archive/) explains how frames move from
  the outbox to your archive, and how they are named and counted.
* [Solvers](/docs/concepts/solvers/) explains how a frame is
  plate-solved, and why the index files stay in one place.
* [Set up a solver](/docs/guides/set-up-a-solver/) loads the index
  files into a claim and declares a `Solver`.

`astrophotography-operator` is one of the extension operators for
[running an observatory](https://liken.sh/docs/concepts/running-an-observatory/).
It drives the telescopes that `observatory-operator` runs, and
`observatory-operator` runs with or without it.

* [The source](https://github.com/liken-sh/liken/tree/main/astrophotography-operator)
* [The design documents](https://github.com/liken-sh/liken/tree/main/astrophotography-operator/plans)
* [The `liken` manual](https://liken.sh/docs/)
