# 30, The film leaves with the lights

The playback pod carries an `ending` label from the moment the sidecar
reports the ending, and the sidecar holds mpv alive for a short grace
after that report, so the compositor has a live surface to fade out.
This is media-operator's part of the theater transition:
display-operator's
[plan 18](https://github.com/liken-sh/display-operator/blob/main/plans/18-a-surface-leaves-with-a-fade.md)
fades a surface that leaves its region, and library-operator's plan 55
dims and brightens the browser under the film.

## The problem

When a film ends today the sidecar publishes the ending and tells mpv
to quit in the same breath. mpv's surface goes within a frame or two,
and the screen cuts from the last frame of the film to whatever is
under it. Under kiosk-shell nothing was under it. Under ivi-shell the
browser is, and a cut from a film to a page is the one thing a theater
never does.

The compositor can fade the film's surface out, and a `Layout` can
say so, but only for a surface that is still alive, and only if it
knows the surface is leaving. Neither is true today: the surface dies
at once, and nothing about the pod says its run is over.

## The design

**The label.** When the operator folds an ending report for a `Play`,
it patches the playback pod's labels with `media.liken.sh/ending:
"true"` before anything else in that pass. A `Layout` whose film
region excludes that label stops matching the pod at once, and
display-operator hides the surface with the region's exit fade. The
label is the operator's vocabulary, the same as
`media.liken.sh/component`, and display-operator never learns it.

**The grace.** The sidecar's `exit` publishes the ending, waits
`exitGrace`, and then sends mpv the quit. The grace is 500 ms: the
label reaches display-operator through the API server and its pod
watch in well under 200 ms, and a 250 ms fade fits in what is left.
mpv keeps drawing the film through the grace, which is what a fade
needs under it. The natural end of the last item and the kubelet's
SIGTERM take the same path, so every ending fades the same way. The
grace is a constant and not a `Player` setting, because a person
never tunes it: it is the compositor's time to act.

A `Layout` that names no exit fade, and a screen with no `Layout`,
see a film that lingers 500 ms after its ending report with the
browser already told `Idle`. The browser's return runs under the film
for that half second and the film then cuts. That is the case the
cluster ran before this plan with the cut half a second earlier, and
the theater `Layout` is one apply away.

## How the work is proved

1. `make test-go` with a test that a folded ending patches the pod's
   labels, and a sidecar test that the quit follows the ending by the
   grace.
2. On `liken-1` with the `theater` `Layout` on the portable panel, a
   film ended with the remote fades out over the browser.

## What is not covered

A pod the kubelet ends cuts instead of fading. The termination signal
reaches mpv's own container at the same moment it reaches the sidecar,
and mpv quits on its own SIGTERM handler, so the surface is gone before
the grace is over. The same holds for the natural end of the last item,
where mpv exits on its own. The grace covers the endings the sidecar
drives mpv through, which is the exit press. A `preStop` hook on the
player container, holding mpv while the sidecar publishes and waits, is
the fix if either case matters.
