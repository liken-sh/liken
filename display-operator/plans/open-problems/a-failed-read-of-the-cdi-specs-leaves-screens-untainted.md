# A failed read of the CDI specs leaves screens untainted

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

The test ran the reconcile pass with a socket that does not answer,
a fake API server that holds one claim on `hdmi-a-1` with a live pod,
and a regular file in place of the CDI directory, so the read of the
specs fails. It asserted that `hdmi-a-1` has a taint in the published
slice. Every other output and draw device got the disconnected taint,
and the test failed with:

```
hdmi-a-1 has no taint while no compositor serves and the CDI specs cannot be read, want the disconnected taint
```

The test is not in the tree, because a failing test cannot land on
`main`. The test asserts the first option below.

## What a fix has to decide

When display-operator cannot read the CDI specs while the compositor
is down, should it taint every screen, which can evict a pod whose
prepare is still running, or keep the slice that it published last?

When the CDI specs cannot be read, the pass can taint every output,
which matches the rule for a failed claim list, or keep the slice it
published last. Tainting every output can evict a pod whose prepare
is still running, which the preparing rule exists to prevent.
