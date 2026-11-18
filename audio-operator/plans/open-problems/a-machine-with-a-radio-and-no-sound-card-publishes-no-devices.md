# A machine with a radio and no sound card publishes no devices

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

The operator's pod runs on a node only when its claim
`every-sound-card` in `audio-operator/deploy/operator.yaml` allocates
at least one device. The claim selects the `sound-card`
`DeviceClass` with `allocationMode: All`, and
`deploy/deviceclasses.yaml` selects every device whose
`sound.liken.sh` attribute `supportsSound` is true. `liken` sets that
attribute on a sound card, and `bluetooth-operator` sets it on the
media bus. So a machine that has a Bluetooth radio and no sound card
runs the operator's pod too.

On that machine, `reconciler.reconcile` in `reconcile.go` reads no
ALSA endpoint, logs "the claimed card has no playback PCM device, so
the pass publishes nothing", and returns before it builds the slice.
`EnsureResourceSlice` in `slices.go` refuses an empty list with
`ErrNoDevices` for the same reason: its comment says that the pod
claims a sound card, so an empty list means that the read failed. The
speakers paired with the radio would then never appear as devices,
although the pod delivers the bus for them.

The check in `reconcile` is `len(outputs) == 0`, and the list holds
capture endpoints as well as playback endpoints, so the log line's
words "no playback PCM device" are narrower than the check. The
wording of that line is being corrected separately.

## What would confirm it

A node with a Bluetooth radio, `bluetooth-operator`, and no sound card,
such as a `lab` machine with the sound card's driver left out of
`spec.modules`, and the operator's log and `ResourceSlice` on it. A
unit test of the reconcile pass with no ALSA endpoint and one paired
speaker shows the shorter fact.

## What a fix has to decide

How the pass tells a failed card read from a machine that has no card.
The claim's allocation names each device that the pod received, so the
pass could treat an empty card read as a failure only when the
allocation holds a sound card. The guard exists because a slice
written from a failed read deletes every device that a prepared claim
still names, and a fix must keep that protection.
