# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It also states how the work was proved, and a proof runs on
hardware, because nothing else proves a design about a sound card.

The pattern these documents build on lives in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/56-device-operators.md),
and this operator's own instance,
[milestone 59](https://github.com/liken-sh/liken/blob/main/plans/59-the-audio-operator.md).

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
* [A consumer's image needs PipeWire's client configuration](open-problems/a-consumers-image-needs-pipewires-client-configuration.md).
  A consumer that carries only `libpipewire-0.3-0` fails before it
  opens the socket, and nothing this operator publishes says so.
* [Every sink is declared stereo](open-problems/every-sink-is-declared-stereo.md).
  The operator declares two channels at `FL,FR` on every output, and
  the PipeWire sources say a node declared without a channel count
  takes the count from the hardware.
* [The claim takes any sound card, and a node serves only one](open-problems/the-claim-takes-any-sound-card-and-a-node-serves-only-one.md).
  The controller claim's `bus == "pci"` pin says which card the
  operator gets and not which card it should get, and the slice is
  named for the node and the driver, so two replicas on one machine
  would overwrite each other.
