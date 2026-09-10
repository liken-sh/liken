# 30, The film leaves with the lights

Built, and drilled on `liken-1` on 2026-09-10 in release
2026.09.10-001. The playback pod carries an `ending` label from the
moment the sidecar reports the ending, and the sidecar holds mpv alive for a short grace
after that report, so the compositor has a live surface to fade out.
This is media-operator's part of the theater transition:
display-operator's
[plan 18](https://github.com/liken-sh/display-operator/blob/main/plans/completed/18-a-surface-leaves-with-a-fade.md)
fades a surface that leaves its region, and library-operator's
[plan 55](https://github.com/liken-sh/library-operator/blob/main/plans/completed/55-lights-down-lights-up.md)
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

## What the drill changed

The drill found a regression in the operator's own timing. The
`Player`'s `Idle` status followed the ending report by 0.6 to 1.4 s,
where it followed by 40 ms before this plan. The move to `Idle` is the
cue the browser brightens on, so the delay held the browser dim after
the film's surface was gone. Two things made it. The pass published
every `Player` status last, behind about fifteen API reads, and the
`ClusterRole` held no `watch` on `players`, so the operator had
re-listed them every two seconds since 2026-08-21.

Commit `7194706` publishes the statuses first and grants the watch.
Three presses then measured 55, 620, and 845 ms, because the two list
reads left in the path run against a k3s API server whose list reads
spike to 800 ms. Commit `b84e295` answers an ending from the lists the
last pass already read, with no read in the path at all. Three presses
then measured `Idle` 2 ms after the ending report, the label within
about 50 ms, and the surface gone at about 580 ms.

Commit `36b8501` passes `--keepaspect-window=no`, in the same release.
A 2.4:1 film rendered stretched, because mpv answered the 1920 by 1080
configure with a 1920 by 800 buffer. mpv now fills the window the
compositor gives it and letterboxes the film inside it.

## How the work is proved

Drilled on `liken-1` on 2026-09-10. On the `dev-003` build of commit
`c86c44a` the ending label landed on the playback pod within 42 to
50 ms of the ending report, and the film's surface left at the 500 ms
grace, measured at 540 to 600 ms. With the `theater` `Layout` on the
portable panel, an exit press ended a film and its surface faded out
over the browser.

The plan as written:

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
