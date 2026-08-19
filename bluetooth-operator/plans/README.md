# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It stays **Proposed** until the thing it describes runs. A
drill proves it, and a drill runs on hardware.

The pattern these documents follow is documented in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md),
and this operator's own instance,
[milestone 58](https://github.com/liken-sh/liken/blob/main/plans/completed/58-the-bluetooth-operator.md).

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents have no number, because nobody has
decided yet what work they become.

## The designs

* [01, Bluetooth audio](01-bluetooth-audio.md). Superseded by
  liken's milestone 60 and plan 05. Its source reading remains the
  citation record.
* [02, The two-container pod](completed/02-the-two-container-pod.md). Built, and
  drilled on liken-1 on 2026-08-17.
* [03, A Secret for each adapter](completed/03-a-secret-for-each-adapter.md).
  Built, and drilled on liken-1 on 2026-08-17. Plan 04 amends its
  Secret layout to one Secret per bond.
* [04, An API for pairing](completed/04-an-api-for-pairing.md). Built, and
  drilled on liken-1 on 2026-08-17. An Adapter and Pairing inventory
  owned by the operator, and a PairingRequest a person opens and
  approves with kubectl. Answers and replaces the open problem "Who
  owns the pairing UX".
* [05, The media bus](completed/05-the-media-bus.md). Built. The adapter's
  media bus as an exclusive DRA device, the hostPath behind the bus
  socket, and the mount-and-variable delivery. This operator's half
  of liken's milestone 60.

## Open problems

* [The restore set is proven for one BR/EDR device](open-problems/the-restore-set-is-proven-for-one-bredr-device.md).
  The adapter's own `identity` file does not travel, and no LE device
  has been through a restore.
* [The operator serves one adapter](open-problems/the-operator-serves-one-adapter.md).
  The bond store and controller discovery are written for one adapter,
  so a node with two adapters serves only the one the claim took.
