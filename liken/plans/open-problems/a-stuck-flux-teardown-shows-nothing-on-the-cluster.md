# A stuck Flux teardown shows nothing on the Cluster

Open problem, recorded on 2026-11-18. It needs a design decision.
The claim was checked against the code on 2026-11-19: `tearDownFlux`
(`cluster-operator/janitor.go:201`) returns nil from each stage that
fails, and returns a condition only from `declinedFluxTeardown`.

When the Cluster no longer declares the flux feature,
`tearDownFlux` in `cluster-operator/janitor.go` deletes the Flux
installation that `liken` created. When a request of the teardown
fails on each pass, the teardown stops at that stage, and the
operator's log gives the error. A person who runs
`kubectl describe cluster` sees no condition, reason, or `Event` that
names the stage where the teardown stopped, or the error. The rule in
`brand/messages.md` for an error that wraps another program's failure
asks for the cause in the log or the status, and here only the log has
it.

## The evidence

`tearDownFlux` returns a condition, of the type `FluxTeardown`, only
when it declines the teardown (`declinedFluxTeardown`,
`cluster-operator/janitor.go:147`). Every other stage that fails
prints a line and returns nil, so no failure reaches the Cluster's
status:

- Stage 0 prints the error of the read of the `flux-system`
  Namespace.
- Stage 1 prints the error of a failed delete of a controller
  `Deployment`.
- Stage 2 prints the error of the list of the Flux controller pods
  (`cluster-operator/janitor.go:256`). A list that fails on each pass,
  for example after a change to the RBAC, stops the teardown before
  stage 3 on every pass.
- Stage 3 prints the error of a patch that removes the finalizers of
  the `Kustomization` and the `GitRepository`, except a 404
  (`cluster-operator/janitor.go:275`). When the patch fails, the
  finalizers stay, no controller is left to remove them, and the
  `flux-system` Namespace stays in the Terminating state.

## What would confirm it

A test of `tearDownFlux` with an API server that refuses the pod
list, and one that refuses the finalizer patch with a 403, which reads
the returned condition across several passes. Today the condition is
nil in both.

## What a fix has to decide

Whether `tearDownFlux` returns a `FluxTeardown` condition that names
the stage and the error while the teardown cannot go on, so that
`kubectl describe cluster` shows a teardown that does not finish, and
which reason each stage gives. A transition of that condition posts
an `Event` through `Recorder.SetCondition`. Decide also whether a
failure that clears on the next pass, such as one refused request,
sets the condition at once or only after it repeats.
