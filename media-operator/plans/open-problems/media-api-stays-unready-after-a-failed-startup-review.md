# `media-api` stays unready after a failed startup review

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
On 2026-11-19 the claim was checked against the code:
`grep -rn 'ready.Store' --include=*.go .` finds one call, at
`apiserve.go:348`, inside `runAPI`. No unit test can run `runAPI`,
because it reads the in-cluster configuration, exits on an error, and
listens on a real socket. A start of `media-api` on the `lab` fleet
while the API server refuses `TokenReview`, followed by a read of
`/readyz` after the API server recovers, would show the defect. If
the startup review moved into a function that takes the authorizer
and returns when the review gets a response, a test with
`apiservertest` could drive it.

`runAPI` in `apiserve.go` calls `apiServerResponds` once, after the
first read of the CA `ConfigMap`s, and sets `server.ready` only when
that one `TokenReview` gets a response. Nothing calls it again. When
the API server is unreachable at that moment, or the service account
token cannot be read, `/readyz` (`serveHealth` in `apidiscovery.go`)
returns 503 for the life of the process.

`deploy/api.yaml` gives the container a `readinessProbe` and no
`livenessProbe`, so the kubelet never restarts the pod. The comment
above the probe says "the pod stays unready until it restarts", and no
part of the system restarts it. The `media-api` `Service` then has no
endpoint until a person deletes the pod. Plan 34 describes `/readyz`
as "a latching startup check", which states the latch and not what
happens when the first check fails.

## Evidence

- `apiserve.go`: `if apiServerResponds(auth) { server.ready.Store(true) }`
  runs once, before `ListenAndServeTLS`.
- `server.ready.Store` has no other caller.
- `deploy/api.yaml` declares only a `readinessProbe`.

## What would confirm it

A test that starts the API with a fake API server that refuses the
first `TokenReview` and accepts later ones, then reads `/readyz` after
the API server recovers. It returns 503.

## What a fix has to decide

Whether the API retries the review until one succeeds and then
latches, or exits so that the kubelet restarts it, or gets a
`livenessProbe` that fails while it is unready. A retry is a loop
with a timer that waits on the API server, so its backoff and its log
lines need the same care as the watch loops.
