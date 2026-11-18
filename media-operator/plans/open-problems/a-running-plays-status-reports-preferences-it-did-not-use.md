# A running Play's status reports preferences it did not use

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`derivePlayStatus` in `status.go` writes `audioLanguages`,
`subtitleLanguages`, `subtitles`, and `sdh` into the Play status on
every pass, from the preferences that `resolvePreferences` returns
then. The CRD (`deploy/plays-crd.yaml`, `status.audioLanguages`)
describes the value as "the audio language order that this run used".
Plan 08 says a change to a tier "takes effect on the next `Play`, not
the one already running", and `ensurePlayback` does not recreate a
pod when only the preferences change. So after an edit to the
`default` `MediaPreferences`, the Player, or the Play, the status of a
running Play shows the new order while its pod runs mpv with the old
one.

## Evidence

- `status.go`, `derivePlayStatus`: `status.AudioLanguages =
  prefs.AudioLanguages` and the same for the other three fields.
- `operate.go`: the pass resolves `prefs` from the current specs, and
  `ensurePlayback` replaces a pod only when it failed or its claim or
  its Remotes changed.

## What would confirm it

A test that runs a Play, changes the `default` `MediaPreferences`, runs
a pass, and compares the status's `audioLanguages` with the pod's mpv
arguments.

## What a fix has to decide

Whether the status reports the preferences that the pod was built with,
read back from the pod, or the current resolution, in which case the
CRD's descriptions change to say so. Plan 08 compares the resolved
preferences with the languages mpv chose, which only works when both
describe the same run.
