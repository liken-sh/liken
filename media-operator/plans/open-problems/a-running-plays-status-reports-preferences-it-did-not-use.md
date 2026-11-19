# A running Play's status reports preferences it did not use

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

A test builds a Play's pod with `buildPod` and an audio language
order of `en`, so its mpv runs with `--alang=en`. It then calls
`derivePlayStatus` for that running pod with the order `de,en`, as a
pass does after an edit to the preferences, and asserts that
`status.audioLanguages` is `[en]`. It failed with:

```
status.audioLanguages = [de en] while the pod runs mpv with --alang=en, want [en]
```

The test is not in the tree, because a failing test cannot land on
`main`. It calls `derivePlayStatus` directly and does not run a whole
pass.

## What a fix has to decide

After you edit the language preferences, the status of a Play that is
already playing shows the new order, and its player still uses the
old one.

Whether the status reports the preferences that the pod was built with,
read back from the pod, or the current resolution, in which case the
CRD's descriptions change to say so. Plan 08 compares the resolved
preferences with the languages mpv chose, which only works when both
describe the same run.
