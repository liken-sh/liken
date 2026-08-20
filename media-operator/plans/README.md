# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It also states how the work was proved, and a proof runs on
hardware. The pattern is documented in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md).

The README states what the operator is. These documents state why it
is built the way it is, and what it still owes an answer to.

## Designs

* [00, The media-operator design](00-design.md). The founding
  design: the `Player`, `Play`, `Remote`, and `Keymap` resources,
  the playback pod, the input bus, and the carriage layer that
  comes later.
* [01, A play becomes a pod](01-a-play-becomes-a-pod.md). Built; the
  drill has not run yet. The first slice: `Player` and `Play`, the
  operator, the player image, and the repository scaffolding. Proved
  when a film plays on `liken-1` from a `kubectl create` and stops
  on the delete.

## Open problems

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents have no number, because nobody
has decided yet what work they become.

* [The player image is still Debian](open-problems/the-player-image-is-still-debian.md).
  The operator image is one binary on `scratch`; the player image is
  a distribution base, because `mpv`'s runtime closure is wide. The
  audio operator's closure-on-scratch treatment applies, with the
  complication that `mpv` loads its GPU drivers only on real
  hardware.
