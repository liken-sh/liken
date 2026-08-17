# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It stays **Proposed** until the thing it describes runs. A
drill proves it, and a drill runs on hardware, because nothing else
proves a design about a radio.

The pattern these documents build on lives in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md),
and this operator's own instance,
[milestone 58](https://github.com/liken-sh/liken/blob/main/plans/completed/58-the-bluetooth-operator.md).

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents carry no number, because nobody has
decided yet what work they become.

## The designs

* [01, Bluetooth audio](01-bluetooth-audio.md). Proposed.
* [02, The two-container pod](02-the-two-container-pod.md). Built, and
  drilled on liken-1 on 2026-08-17.
* [03, A Secret for each adapter](03-a-secret-for-each-adapter.md).
  Proposed.

## Open problems

* [Who owns the pairing UX](open-problems/who-owns-the-pairing-ux.md).
  The operator runs bluetoothd, so it is the only layer that can offer
  pairing, and whether it should is not decided.
* [The restore set is proven for one BR/EDR device](open-problems/the-restore-set-is-proven-for-one-bredr-device.md).
  The adapter's own `identity` file does not travel, no LE device has
  been through a restore, and `ReadTree` discards an unreadable bond
  while failing on an unreadable cache entry.
