# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It also states how the work was proved, and a proof runs on
hardware.

The pattern these documents build on lives in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md),
and this operator's own instance,
[milestone 59](https://github.com/liken-sh/liken/blob/main/plans/completed/59-the-audio-operator.md).

The README states how to use the operator. These documents state why it
is built the way it is, and what it still owes an answer to.

## Designs

* [01, The taints an output carries](01-the-taints-an-output-carries.md).
  Built.

## Open problems

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents carry no number, because nobody has
decided yet what work they become.

* [The ELD carries no serial number](open-problems/the-eld-carries-no-serial-number.md).
  An audio device names the model of monitor it plays into, and it
  cannot name the unit, so two identical monitors share one pairing
  identity.
* [The image is still Debian](open-problems/the-image-is-still-debian.md).
  The two sibling operators ship images built from a named set of
  files, and this one installs its daemons from a distribution.
* [A sink can be shared and this one is not](open-problems/a-sink-can-be-shared-and-this-one-is-not.md).
  PipeWire mixes streams and every device this operator publishes is
  exclusive, so the second pod to claim a sink waits behind the first.
* [The analog jack publishes whether or not anything is plugged in](open-problems/the-analog-jack-publishes-whether-or-not-anything-is-plugged-in.md).
  A dark HDMI connector publishes with two taints and the analog jack
  publishes with none, which may make this a missing taint rather than
  an extra device.
