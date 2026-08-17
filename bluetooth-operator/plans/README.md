# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It stays **Proposed** until the thing it describes runs. A
drill proves it, and a drill runs on hardware, because nothing else
proves a design about a radio.

The pattern these documents build on lives in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/56-device-operators.md),
and this operator's own instance,
[milestone 58](https://github.com/liken-sh/liken/blob/main/plans/58-the-bluetooth-operator.md).

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents carry no number, because nobody has
decided yet what work they become.

## The designs

* [01, Bluetooth audio](01-bluetooth-audio.md). Proposed.
* [02, The two-container pod](02-the-two-container-pod.md). Built, and
  drilled on liken-1 on 2026-08-17.

## Open problems

* [Bonds follow the pod's ordinal and adapters do not](open-problems/bonds-follow-the-ordinal-and-adapters-do-not.md).
  A link key belongs to one adapter, and the StatefulSet gives a
  replica its bonds volume by ordinal.
* [Whether the bonds belong in the cluster](open-problems/whether-the-bonds-belong-in-the-cluster.md).
  The workload is a StatefulSet because the bonds live on a volume,
  and nobody has priced keeping them in the API instead.
* [Who owns the pairing UX](open-problems/who-owns-the-pairing-ux.md).
  The operator runs bluetoothd, so it is the only layer that can offer
  pairing, and whether it should is not decided.
* [Inspecting a pod with no tools](open-problems/inspecting-a-pod-with-no-tools.md).
  The bluetoothd image carries four binaries and no shell, so the
  ordinary way to read a pod's state does not run.
