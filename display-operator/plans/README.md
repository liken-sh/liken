# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It separates what was measured from what was only read, and
it names where the measurement ran.

The pattern these documents build on lives in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md),
and this operator's own instance,
[milestone 57](https://github.com/liken-sh/liken/blob/main/plans/completed/57-the-display-operator.md).

The [README](../README.md) says how to deploy the operator and how to
claim an output. These documents say why it is built the way it is.

## Designs

* [01, The compositor image](01-the-compositor-image.md). Built. The
  image is a library closure on scratch, and the pod runs one
  container.

## Open problems

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents carry no number, because nobody has
decided yet what work they become.

* [Routing is narrower than inventory](open-problems/routing-is-narrower-than-inventory.md).
  Every connector publishes as a device, and only the connectors that
  had a monitor at startup have an output in the compositor's
  configuration.
* [A removed device has no bounded retry](open-problems/a-removed-device-has-no-bounded-retry.md).
  The kubelet retries `NodePrepareResources` without a bound for a
  device an allocation names but the slice no longer lists, so the
  operator taints a dark output and never deletes it.
* [The library closure carries loads that `ldd` cannot see](open-problems/loads-that-ldd-cannot-see.md).
  Four kinds of load and four data paths were added by hand, and
  nothing in the build finds the next one.
* [LLVM is two thirds of the image](open-problems/llvm-is-two-thirds-of-the-image.md).
  The image is already on scratch, and 68% of it is LLVM and the
  libraries only LLVM needs, because Debian builds mesa with llvmpipe
  and no liken machine runs llvmpipe.
* [The compositor drives one card](open-problems/the-compositor-drives-one-card.md).
  One Weston binds one DRM device, so a node with two graphics cards
  serves only the card the claim took.
