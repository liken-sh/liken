---
title: astrophotography-operator
---

# `astrophotography-operator`

`astrophotography-operator` takes the pictures you ask for on a
telescope that [`observatory-operator`](https://liken.sh/observatory/)
runs. You declare a `Target`: an object, its coordinates, and the
frames you want of it, such as sixty five-minute frames of M42
through each of three filters. When you reserve the telescope for the
operator, it slews to the target, centers it with a plate solve,
guides with PHD2, and takes the frames. It moves each frame to the
archive you chose, names it the way you asked, and records it in a
catalog beside the frames. A target can take many nights, and its
status keeps the count.

Things you can do with it:

* Image one object across several nights, and pick up where the last
  night stopped.
* Queue several targets, and let the highest priority go first.
* Name each frame by object, night, and filter, so a stacking tool
  finds a night's frames in one directory.
* Keep frames on a network share or a disk of your choice, and check
  each one against the SHA-256 the catalog recorded.

The operator slews, centers, guides, dithers, exposes, and moves
frames to the archive, and it skips a target that is below the
telescope's horizon. It images the eligible target with the highest
priority, and does not yet weigh the moon, twilight, or how soon a
target sets. It does not reserve the telescope on its own, grade
frames, or take calibration frames. Its tests and drills run against
the INDI simulators, and no drill has run on real equipment yet. The
design and the work still to come are in the
[plans](https://github.com/liken-sh/liken/tree/main/astrophotography-operator/plans).

Start here:

* [Install the operator](/docs/guides/install/) beside
  `observatory-operator`.
* [Declare a target](/docs/guides/declare-a-target/) and the archive
  for its frames.
* [Set up a solver](/docs/guides/set-up-a-solver/), if you want each
  target centered.
* [Image a target](/docs/guides/image-a-target/): reserve a telescope
  for the operator and follow the night.
* [Find and verify your frames](/docs/guides/find-your-frames/), and
  copy them to your computer.
* [Troubleshoot](/docs/guides/troubleshoot/) a target that does not
  start, or a frame that does not reach the archive.

`astrophotography-operator` is one of the extension operators for
[running an observatory](https://liken.sh/docs/concepts/running-an-observatory/).
`observatory-operator` runs the equipment, and this operator chooses
the targets and takes the frames. `observatory-operator` runs with or
without it.

* [The source](https://github.com/liken-sh/liken/tree/main/astrophotography-operator)
* [The design documents](https://github.com/liken-sh/liken/tree/main/astrophotography-operator/plans)
* [The `liken` manual](https://liken.sh/docs/)
