# 30, Delay playback exit for a fade

Built, and drilled on `liken-1` on 2026-09-10 in release
2026.09.10-001. When the sidecar reports the ending, the operator adds
an `ending` label to the playback pod. The sidecar keeps mpv alive for
a short grace after that report, so the compositor has a live surface
to fade.
This is media-operator's part of the theater transition:
display-operator's
[plan 18](https://github.com/liken-sh/display-operator/blob/main/plans/completed/18-a-surface-leaves-with-a-fade.md)
fades a surface that leaves its region, and library-operator's
[plan 55](https://github.com/liken-sh/library-operator/blob/main/plans/completed/55-browser-dimming-during-playback.md)
dims and brightens the browser under the film.

## The problem

When a film ends today, the sidecar publishes the ending and tells mpv
to quit immediately. mpv's surface disappears within one or two frames.
The screen then cuts from the film's last frame to the surface below it.
Under kiosk-shell, no surface was below it. Under ivi-shell, the
browser was below it. The theater transition should fade between the
film and the browser.

The compositor can fade the film's surface out, and a `Layout` can
request that fade, but only while the surface is alive and the
compositor knows that the surface is leaving. Neither condition holds
today. The surface ends immediately, and the pod has no ending label.

## The design

**The label.** When the operator folds an ending report for a `Play`,
it patches the playback pod's labels with `media.liken.sh/ending:
"true"` before anything else in that pass. A `Layout` whose film region
excludes that label stops matching the pod at once. display-operator
then hides the surface with the region's exit fade. media-operator
defines this label, as it defines `media.liken.sh/component`.
display-operator applies the `Layout` selector without treating this
label specially.

**The grace.** The sidecar's `exit` publishes the ending, waits
`exitGrace`, and then sends mpv the quit. The grace is 500 ms. The
label reaches display-operator through the API server and its pod watch
in well under 200 ms, leaving time for a 250 ms fade. mpv keeps drawing
the film through the grace, which is what a fade needs under it. The
natural end of the last item and the kubelet's SIGTERM take the same
path, so every ending fades the same way. The grace is a constant, not
a `Player` setting, because a person never tunes it. It is the
compositor's time to act.

Under a `Layout` with no exit fade, or without a `Layout`, the film
remains visible for 500 ms after its ending report. The browser has
already received `Idle` and returns below the film during that half
second. The film then cuts. Before this plan, the cluster ran this case
with the cut half a second earlier. Apply the theater `Layout` to
enable the fade.

## What the drill changed

The drill found a regression in the operator's timing. The `Player`'s
`Idle` status followed the ending report by 0.6 to 1.4 s, where it
followed by 40 ms before this plan. The browser brightens when it
receives the move to `Idle`, so the delay kept the browser dim after
the film's surface was gone. Two factors caused the delay. The pass
published every `Player` status after about fifteen API reads, and the
`ClusterRole` had no `watch` on `players`, so the operator re-listed
them every two seconds since 2026-08-21.

Commit `7194706` publishes the statuses first and grants the watch.
Three presses then measured 55, 620, and 845 ms. The remaining delay
came from the two list reads left in the path, which run against a k3s
API server whose list reads spike to 800 ms. Commit `b84e295` answers
an ending from the lists the last pass already read, with no read in
the path. Three presses then measured `Idle` 2 ms after the ending
report, the label within about 50 ms, and the surface gone at about
580 ms.

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
2. On `liken-1` with the `theater` `Layout` on the portable panel,
   ending a film with the remote makes its surface fade out over the
   browser.

## What is not covered

A pod the kubelet ends cuts instead of fading. The termination signal
reaches mpv's own container at the same moment it reaches the sidecar,
and mpv quits on its own SIGTERM handler, so the surface is gone before
the grace is over. The same holds for the natural end of the last item,
where mpv exits on its own. The grace covers the endings the sidecar
drives mpv through, which is the exit press. A `preStop` hook on the
player container, holding mpv while the sidecar publishes and waits, is
the fix if either case matters.
