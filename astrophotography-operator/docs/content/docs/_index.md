---
title: Manual
---

# Manual

`astrophotography-operator` images the targets you declare on a
telescope that `observatory-operator` runs. To take your first frames,
follow the guides in order:

1. [Install the operator](/docs/guides/install/).
2. [Declare a target](/docs/guides/declare-a-target/).
3. [Set up a solver](/docs/guides/set-up-a-solver/), if the target
   should be centered.
4. [Image a target](/docs/guides/image-a-target/).
5. [Find and verify your frames](/docs/guides/find-your-frames/).

[Troubleshoot](/docs/guides/troubleshoot/) lists each condition reason
and `Event` with what to fix.

The concepts explain what the guides do. [Targets](/docs/concepts/targets/)
explains each field of a `Target`, [Imaging](/docs/concepts/imaging/)
explains each step of a night, [The archive](/docs/concepts/archive/)
explains how a frame moves from the outbox to your archive, and
[Solvers](/docs/concepts/solvers/) explains how a frame is plate-solved. The reference lists the Prometheus metrics and
every field of `Target` and `Solver`.

`observatory-operator` manages the equipment and the reservations, and
starts and stops the hardware at each end of a night. Its
[manual](https://liken.sh/observatory/) covers them.
