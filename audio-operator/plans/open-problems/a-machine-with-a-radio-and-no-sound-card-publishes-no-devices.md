# A machine with a radio and no sound card publishes no devices

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

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
ALSA endpoint, logs "the claimed cards have no PCM device, so the pass
publishes nothing", and returns before it builds the slice.
`EnsureResourceSlice` in `slices.go` refuses an empty list with
`ErrNoDevices` for the same reason: its comment says that the pod
claims a sound card, so an empty list means that the read failed. The
speakers paired with the radio would then never appear as devices,
although the pod delivers the bus for them.

## The reproduction

The test ran one reconcile pass over the fake API server with a
`/dev/snd` that holds `controlC0`, `timer`, and `seq` and no PCM
device, a graph with one Bluetooth speaker, and bluetoothd reporting
that speaker as paired. It asserted that the pass creates a
`ResourceSlice` that holds the speaker, and it failed with:

    the pass created no ResourceSlice, want one with the paired speaker

The test is not in the tree, because a failing test cannot land on
`main`. A node with a Bluetooth radio, `bluetooth-operator`, and no
sound card, such as a `lab` machine with the sound card's driver left
out of `spec.modules`, would show the result on a real machine, in the
operator's log and its `ResourceSlice`. That drill has not run.

## What a fix has to decide

On a machine whose claim got only the Bluetooth media bus, should the
operator publish the paired speakers? One option publishes them, and
the pass then has to tell a failed card read from a machine that has
no card. The claim's allocation names each device that the pod
received, so the pass could treat an empty card read as a failure
only when the allocation holds a sound card. Another option keeps the
operator's pod off a machine with no sound card, so the speakers of
that machine's radio are not published. The guard exists because a slice
written from a failed read deletes every device that a prepared claim
still names, and a fix must keep that protection.
