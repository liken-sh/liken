# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It separates what was measured from what was only read, and
it names where the measurement ran.

The pattern these documents follow is documented in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md),
and this operator's own instance,
[milestone 57](https://github.com/liken-sh/liken/blob/main/plans/completed/57-the-display-operator.md).

The manual at [display.liken.sh](https://display.liken.sh) says how
to deploy the operator and how to claim an output. These documents
say why it is built the way it is.

## Designs

* [01, The compositor image](completed/01-the-compositor-image.md). Built. The
  image is a library closure on scratch, and the pod runs one
  container.
* [02, An output for every connector](completed/02-an-output-for-every-connector.md).
  Built, and drilled on liken-1 on 2026-08-17. Every connector gets an
  `[output]` section at startup. A preload shim moves the compositor's
  hotplug subscription to the kernel's own netlink group, so a monitor
  that arrives on a dark connector routes without a restart. Answers
  and replaces the open problem "Routing is narrower than inventory".

## Open problems

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents have no number, because nobody has
decided yet what work they become.

* [The library closure includes loads that `ldd` cannot report](open-problems/loads-that-ldd-cannot-see.md).
  Four kinds of load and four data paths were added by hand, and
  nothing in the build reports the next one.
* [LLVM is two thirds of the image](open-problems/llvm-is-two-thirds-of-the-image.md).
  The image is already on scratch, and 68% of it is LLVM and the
  libraries only LLVM needs, because Debian builds mesa with llvmpipe
  and no liken machine runs llvmpipe.
* [The compositor drives one card](open-problems/the-compositor-drives-one-card.md).
  One Weston binds one DRM device, so a node with two graphics cards
  serves only the card the claim took.
