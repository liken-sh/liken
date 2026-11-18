# A failed read of the CDI specs leaves screens untainted

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When no compositor serves its socket, the reconcile pass in `main.go`
puts the disconnected taint on every output and draw device, so the
taint-eviction controller deletes the pods whose Wayland connections
ended. An output whose claim is still preparing keeps no taint.
`preparingOutputs(allocated, claimed)` finds those outputs: an output
that a live pod's claim holds and that no CDI spec on disk lists.

`claimed` comes from `preparedOutputs()`, which returns `nil` when it
cannot read the CDI directory. The pass logs the error and goes on.
`preparingOutputs` then treats every allocated output as preparing,
so `compositorDown` leaves the output device of every held screen
untainted, and the pods that hold those output devices are not
evicted while the compositor is down.

## Evidence

- `main.go`, `reconcile`: `claimed, err := preparedOutputs()` logs
  "reading the claims the kubelet prepared" and keeps `claimed` as
  `nil`.
- `screenholds.go`, `preparingOutputs`: `if !prepared[output]` is true
  for every output when `prepared` is `nil`.
- The comment in `reconcile` states the opposite rule for the other
  read: "When the ResourceClaims cannot be listed, every output is
  tainted."

## What would confirm it

A test of the reconcile pass with no compositor, one claim held by a
live pod, and a CDI directory that cannot be read. The slice should
taint that claim's output.

## What a fix has to decide

When the CDI specs cannot be read, the pass can taint every output,
which matches the rule for a failed claim list, or keep the slice it
published last. Tainting every output can evict a pod whose prepare
is still running, which the preparing rule exists to prevent.
