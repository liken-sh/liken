# A bond is lost when the pod stops before the pass that stores it

Open bug, recorded on 2026-11-20. A unit test reproduced it on
2026-11-20.

When you pair a device, BlueZ writes the bond to its own directory
in the pod. That directory is an `emptyDir`, so it is gone when the
pod is deleted. The operator copies each bond from the directory to a
`Secret` during a pass, and a pass is the only thing that does. When
the pod gets a shutdown signal, the operator's loop returns at once
and runs no pass. So a bond that BlueZ wrote after the last pass is
never copied. The next pod starts with the bonds that the `Secret`s
have, and the device has to be paired again.

## Evidence

- The `bonds` volume of the pod is an `emptyDir` in
  `deploy/operator.yaml`. The comment there says that the `Secret`s
  are what a new pod gets its bonds from.
- `persist` in `bondstore.go` is the only code that writes a bond to
  a `Secret`, and a pass calls it.
- `loop` in `main.go` returns `errShutdown` as soon as the context is
  cancelled. It calls no pass on that path.
- A pass does not run when BlueZ writes the bond. It runs after the
  signals have been quiet for `settleWindow`, so that it does not
  read a file that BlueZ is still writing. A pod that stops inside
  that window has a bond on disk and no pass that copies it.

A second case may have the same result, and no test has checked it.
`persist` skips a bond that has no `Peripheral`, which
`TestPersistSkipsABondWithNoPeripheral` checks. If the pod stops
before the `Peripheral` of a newly paired device exists, that bond is
not copied either.

## The reproduction

A test started `loop` with a pass that counts its calls, cancelled
the context, and waited for `loop` to return. It checked that `loop`
returned `errShutdown` and that the pass ran once after the signal.
It failed:

```
the loop ran 0 passes after the shutdown signal, want 1
```

The test is not in the tree.

## What a fix has to decide

- **Whether the loop runs one last pass when it stops.** The pass
  would have to finish inside the pod's termination grace period, and
  it sends requests to the API server while the pod stops.
- **Whether that pass waits for BlueZ.** A pass that runs at once can
  read a bond file that BlueZ has not finished, which `settleWindow`
  exists to avoid.
- **What a person sees when a bond is lost anyway**, such as after a
  node loses power, and whether the `Peripheral` should report it.
