# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It also states how the work was proved, and a proof runs on
hardware.

The pattern these documents follow is documented in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md),
and this operator's own instance,
[milestone 59](https://github.com/liken-sh/liken/blob/main/plans/completed/59-the-audio-operator.md).

The README states how to use the operator. These documents state why it
is built the way it is, and what it still owes an answer to.

## Designs

* [01, The taints on an output](completed/01-the-taints-an-output-carries.md).
  Built.
* [02, A closure on scratch](completed/02-a-closure-on-scratch.md).
  Built, and drilled on liken-1 on 2026-08-17. The image becomes the
  display operator's shape: a named file set on `scratch`, measured
  from the running daemons' memory maps, with a release gate that
  starts the daemons and fails on any file they map that the image
  lacks. Answers and replaces the open problem "The image is still
  Debian".
* [03, The kubelet supervises the daemons](completed/03-the-kubelet-supervises-the-daemons.md).
  Built, and drilled on liken-1 on 2026-08-17, the fault drill
  included. The pod becomes four containers: a declare init step, the
  two daemons as native sidecars with real-client probes, and the
  operator. The die-together guarantee survives as an all-devices
  taint the operator publishes when it loses the graph. The drill
  proved it end to end, from the cross-namespace access defect that
  `config/51-access-rules.conf` answers to the eviction that a
  sustained outage causes.
* [04, Bluetooth sinks](completed/04-bluetooth-sinks.md). Built,
  and drilled on liken-1 on 2026-08-19, both halves. The audio half
  of liken's milestone 60: the declare container enables
  WirePlumber's bluez monitor when the claim delivered a media bus,
  and each paired speaker publishes as an `audio.liken.sh` device
  beside the card's outputs. The drill opened the open problem
  "A2DP does not survive a Bluetooth pod restart".
* [05, Choosing the codec](completed/05-choosing-the-codec.md).
  Built and drilled on liken-1 on 2026-08-19, in release
  2026.08.19-007. Each connected speaker publishes its codecs, a
  claim states one through opaque config resolved on the
  allocation, and every delivered sink arrives at unity volume.
* [06, Restarting WirePlumber when the bus dies](completed/06-restarting-wireplumber-when-the-bus-dies.md).
  Built, and drilled on liken-1 on 2026-08-21, in release
  2026.08.21-002. A liveness probe on the WirePlumber container reads
  whether the adapter still advertises a media profile that
  bluetoothd hosts, so the kubelet restarts the one container whose
  registration is gone. The drill measured 35 seconds from the loss
  to the restart, with PipeWire and the card's sinks left running.
  Answers and removes the open problem "A2DP does not survive a
  Bluetooth pod restart".

## Open problems

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents have no number, because nobody has
decided yet what work they become.

* [A sink can be shared and this one is not](open-problems/a-sink-can-be-shared-and-this-one-is-not.md).
  PipeWire mixes streams and every device this operator publishes is
  exclusive, so the second pod to claim a sink waits behind the first.
