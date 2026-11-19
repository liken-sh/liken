# The Flux teardown drops two errors

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When the Cluster no longer declares the flux feature,
`tearDownFlux` in `cluster-operator/janitor.go` deletes the Flux
installation that `liken` created. Two of its API requests can fail
with no log line and no condition, so the teardown stops and a
person cannot see why. The rule "Errors include their source's text"
in `liken/AGENTS.md` asks for the cause in the log or the status.

## The evidence

- Stage 2 lists the Flux controller pods with
  `kubernetes.List[featureWorkload](c, fluxControllerPodsPath)`, and
  `if err != nil || len(pods) > 0 { return nil }` ends the pass on an
  error exactly as it does while pods still exist. A list that fails
  on each pass, for example after a change to the RBAC, stops the
  teardown before stage 3 on every pass.
- Stage 3 removes the finalizers of the `Kustomization` and the
  `GitRepository` with
  `_ = kubernetes.PatchJSON(c, path, ...)`. A 404 is expected there,
  because a later pass finds the objects deleted. Any other error is
  discarded too. When the patch fails, the finalizers stay, no
  controller is left to remove them, and the `flux-system` Namespace
  stays in the Terminating state.

`tearDownFlux` returns a condition only when it declines the
teardown (`declinedFluxTeardown`). The other failures of the
function print a line and return nil, so no failure reaches the
Cluster's status.

## What would confirm it

A test of `tearDownFlux` with an API server that refuses the pod list,
and one that refuses the finalizer patch with a 403, which reads the
output and the returned condition across several passes.

## What a fix has to decide

Whether each failure prints the error, as the other stages do, or
whether `tearDownFlux` returns a condition that names the stage and
the error, so that `kubectl get cluster` shows a teardown that does
not finish. Decide which errors of the finalizer patch are expected
and stay silent.
