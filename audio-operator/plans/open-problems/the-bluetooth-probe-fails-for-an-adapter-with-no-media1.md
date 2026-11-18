# The Bluetooth probe fails for an adapter with no `Media1`

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`endpointsRegistered` in `audio-operator/endpoints.go` is the probe
that restarts the WirePlumber container when bluetoothd no longer
advertises WirePlumber's media endpoints. It reads bluetoothd's
managed objects and calls `endpointsFrom`, which counts the adapters
and reports whether any adapter advertises a media profile that its
`org.bluez.Media1` interface hosts.

`endpointsFrom` counts an adapter that has no `Media1` interface, and
never marks it registered. When that adapter is the only one, the
result is one adapter and `registered` false, so the probe calls
`fatal` and exits 1. The kubelet then restarts WirePlumber. A restart
does not add `Media1` to the adapter, so the probe fails again after
each restart, and the container restarts in a loop. The message also
reads "the adapter advertises none of the 0 media profile(s)
bluetoothd hosts", because `hostedProfiles` counts the profiles of
`Media1` interfaces and there are none.

The case in `endpoints_test.go` for an adapter with no `Media1`
expects `registered` false, so the tests pass with this behavior.

## What would confirm it

A machine whose bluetoothd publishes an adapter without `Media1`, and
the restart count of the WirePlumber container on it. It is not known
when bluetoothd publishes an adapter that way: an adapter that is
still starting, an adapter that is powered off, or a bluetoothd built
without the media plugin are candidates.

## What a fix has to decide

Whether an adapter with no `Media1` counts as an adapter for the
probe. When it does not, a machine whose adapters all lack `Media1`
takes the branch for no adapter, which passes. When it does, the
probe needs a reason to expect a restart to help.
