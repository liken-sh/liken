# A surface leaves with a fade

Plan 18. A region's transition gains an exit half, the module's `hide`
carries a transition, and a surface that stops matching a region fades
out instead of vanishing. This is the compositor's part of the theater
transition: the media browser dims as a film starts, the film fades in
over it, and at the end the film fades out and the browser brightens.
media-operator's plan 30 and library-operator's plan 55 are the other
two parts.

## The problem

Plan 17 fades a surface in and moves it, and it hides a surface with a
cut. A film that ends takes its surface with it, and the compositor
cannot fade what no longer exists, so the plan left the exit to the
workload. That holds for a workload that fades its own picture, and a
hardware-decoded film under `vo=dmabuf-wayland` has no picture path to
fade on: the frames go from the decoder to the compositor untouched.

The compositor can fade a surface that is still alive. What it needs is
a moment between "this surface is leaving" and "this surface is gone",
and a placement that says fade during it.

## The design

`Layout.spec.regions[].transition` becomes two halves, `enter` and
`exit`, each `{kind: none|fade, milliseconds}`. Both are optional and
absent means at once. The flat `{kind, milliseconds}` form is dropped:
nothing released reads it.

The module's `hide` takes the same two trailing tokens `place` does,
`<transition> <ms>`, and a `fade` hide sets
`IVI_LAYOUT_TRANSITION_VIEW_FADE_ONLY` before the visibility change, so
ivi-layout runs its visibility-off fade. The smoke test proves a hide
with a fade leaves pixels in the rectangle at the fade's midpoint and
none after it.

The placement pass sends the region's exit transition when a surface
that was placed in a region is no longer placed there and still exists:
it stopped matching, the region's selector changed, or the `Layout`
changed under it. A surface the module reported gone is gone and gets
nothing. A surface that moves from one region to another gets the new
region's enter transition as a move, the same as today.

The default layout has no transitions, so a screen that names no
`Layout` is unchanged.

The media browser's theater is one `Layout`:

```yaml
apiVersion: display.liken.sh/v1alpha1
kind: Layout
metadata:
  name: theater
spec:
  regions:
    - name: room
      rect: {left: 0, top: 0, width: 1, height: 1}
      selector:
        matchLabels: {app.kubernetes.io/name: library-media-browser}
    - name: film
      rect: {left: 0, top: 0, width: 1, height: 1}
      selector:
        matchExpressions:
          - {key: media.liken.sh/component, operator: In, values: [playback]}
          - {key: media.liken.sh/ending, operator: DoesNotExist}
      transition:
        enter: {kind: fade, milliseconds: 600}
        exit: {kind: fade, milliseconds: 250}
```

The film region stops matching the moment media-operator labels the
playback pod `media.liken.sh/ending`, which its plan 30 does when the
sidecar reports the ending, and the sidecar holds mpv alive for the
fade. The pass runs on the pod watch with no settle, so the fade starts
within the pod watch's latency of the label.

## How the work is proved

1. `make test`, and the smoke test's new hide-with-fade case.
2. On `liken-1`, the `theater` `Layout` on the portable panel: a film
   started from the browser fades in over the dimmed page, and a film
   ended with the remote fades out over it.
