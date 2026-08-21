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
* [03, The modes a monitor accepts](completed/03-the-modes-a-monitor-accepts.md).
  Built, and adopted on liken-1 on 2026-08-19. Each connected output
  publishes a `modes` attribute: the kernel's list, deduplicated, cut
  to whole names under the API's 64-character limit.
* [04, The kubelet supervises the compositor](completed/04-the-kubelet-supervises-the-compositor.md).
  Built and drilled on liken-1 on 2026-08-19. Weston moves to a container of its own, the
  kubelet restarts it alone, and the operator taints every output
  while nothing answers on the compositor's socket. The prerequisite
  for a claim that selects a mode.
* [05, Choosing the mode](completed/05-choosing-the-mode.md). Built
  and drilled on liken-1 on 2026-08-19. A claim's opaque config
  states a resolution, and the operator rewrites the compositor's
  config, restarts it, and delivers only after the readback reports
  the mode.
* [06, Matching the refresh](completed/06-matching-the-refresh.md).
  Built, and drilled on liken-1 on 2026-08-19. The mode grows an
  integer refresh, validated against the kernel's own mode list, so
  a 24 fps film runs on a 24 Hz mode. Absorbs and replaces the open
  problem "A mode list read too early goes stale": every prepare
  that reached the card republishes the slice.

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
* [A blinked output loses its surface](open-problems/a-blinked-output-loses-its-surface.md).
  When a monitor's link drops and returns, kiosk-shell moves the
  fullscreen surface to a surviving output and never moves it back, so
  the movie stays on the wrong screen.
* [A control claim waits for the compositor](open-problems/a-control-claim-waits-for-the-compositor.md).
  DDC/CI runs to the panel with no compositor, but `prepareClaim` and a
  second gate hold every claim on Weston's socket, so a control-only pod
  waits for a compositor it does not need.
