# The catalog on a screen

Plan 06. The media browser as a `Player`'s idle screen, in a pod this
operator builds: the browser, the Corrosion sidecar with the catalog on
the screen's own disk, and the update stream the browser reads. At the
end of this plan a `Player` that names this operator as its idle
controller shows the namespace's libraries on its screen, and a title
added on the volume reaches that screen with no request from the
browser.

## The problem

The media browser reads the catalog from a local file, and an agent in
the same pod keeps that file. The idle pod was `media-operator`'s, with
exactly the containers `media-operator` gave it. An earlier draft of
this plan asked `media-operator` for a passthrough: extra containers
and volumes declared on the `Player`. That made the lower layer carry
this layer's pod definition without naming it.

`media-operator`'s plan 22 answers it the other way. A `Player` names
its idle controller, `spec.idle.controller`, and when the name is not
`media-operator`'s own, `media-operator` stands the display claim and
the idle command pod and no client pod. It publishes what a delegate
needs on the `Player` status:

    status:
      idle:
        controller: library.liken.sh/media-browser
        claim: studio-lg-idle-devices
        requests: [draw, render]

This operator is the first delegate. Its name is
`library.liken.sh/media-browser`.

## The operator reads Players

The operator lists and watches `Player` in `media.liken.sh/v1alpha1`,
cluster-wide, read-only. It never writes a `Player`. The watch is a
wake, like the Library watch, and every pass re-lists.

A pass acts on `status.idle` and never on `spec.idle`. The spec may
inherit its controller from `MediaPreferences`, and only
`media-operator` resolves the tiers. A `Player` whose
`status.idle.controller` is this operator's name gets a screen pod. Any
other value, or no `status.idle` at all, gets none, and a screen pod
that stands for such a `Player` is deleted. `media-operator` deletes
this operator's pod itself when the claim must be replaced, so the
switch away is the only delete this plan adds.

## The screen pod

One pod per delegated `Player`, `<player>-media-browser`, in the
`Player`'s namespace and owned by the `Player`, so deleting the
`Player` tears it down. It follows the template hash the scanner pods
follow, and a pod that is missing on a pass is created, which is also
how a pod `media-operator` evicted comes back. No `Deployment`.

The pod holds:

* The Corrosion sidecar, a native sidecar as in the scanner pod, with
  its state on an `emptyDir`. It joins the namespace's catalog cluster
  through the `catalog` Service and rebuilds from its peers on every
  start. The proof of concept measured that rebuild: a fresh node
  joining a cluster with 105,000 rows was in full sync after 17 s. The
  first full sync leaves the agent at its ingest peak, up to 380 MB,
  until it restarts, after which it uses 74 MB with the same rows. A
  busy agent can take more than 30 s to stop, so the grace period is
  set long enough.
* The browser, from the image `BROWSER_IMAGE` names on the operator's
  Deployment, `ghcr.io/liken-sh/library-operator-media-browser`. Its
  arguments are `--catalog` on the sidecar's file, `--updates` on the
  sidecar's loopback API, and one `--library-root <namespace>/<name>=<mount>`
  per Library in the namespace.
* Every Library's storage claim in the namespace, mounted read-only
  under one path per Library, at the Library's `storage.root`. The
  browser draws poster art from those mounts.
* The display claim. `resourceClaims` names `status.idle.claim`, and
  the browser container states one `resources.claims` entry per name in
  `status.idle.requests`.

The pod mounts no ServiceAccount token, and both containers run
unprivileged, as the scanner pod does. The operator adds the screen
pod to the namespace's catalog `EndpointSlice`, so a screen is a
bootstrap peer for the next screen when no scanner is up.

## The browser on a claimed screen

The draw device delivers two variables into the container through CDI:
`WAYLAND_DISPLAY`, the compositor socket, and `DISPLAY_APP_ID`, the
app-id of the allocated output. The browser asks for its window with
that app-id, because the compositor places a window on the claimed
output by app-id. Today the browser reads only `WAYLAND_DISPLAY`, so
this plan adds the app-id.

The browser also arms the window watchdog `media-operator`'s idle client
has: when the compositor gives no window within a grace, the process
exits with code 7 and the kubelet restarts it with backoff. The grace
arrives in `WINDOW_GRACE_SECONDS`, and the operator sets it to 15. The
watchdog and the app-id option port from `media-operator/idle`.

## What is left for later

The browser reads no bus in this plan. Remote keys, the built-in idle
view, and playback from the wall follow in plans 07 and 08. At the end
of this plan the screen shows the wall and takes no input.

## The local harness

`local/browse` already runs the browser against the workstation's
three-agent cluster. This plan changes nothing there.

## What was set aside

Extra containers on `media-operator`'s idle pod, the earlier draft of
this plan, for the reason above.

A `Deployment` per screen. The pod's recreate is already this
operator's pass, and a `Deployment` would add an API group and a
verb for a job the scanner pods do without one.

A catalog mounted from a shared volume instead of a sidecar. That was
the Litestream design, in [`rejected/`](rejected/).

Reads through the sidecar's API. The API is for writes and for the
stream. Reads from the file measured under 4 ms.

## Proof

On `liken-1`: a `Player` set to `library.liken.sh/media-browser`
stands a screen pod, the sidecar joins the scanners' cluster and
receives their rows, and the browser draws the wall with poster art on
the screen. A title added on the volume and indexed by a scanner
appears on the screen within a few seconds. `media-operator`'s plan 22
drill replaces the claim under the pod, and the pod comes back and
draws again. The sidecar's resident memory after its restart is
recorded on the box.
