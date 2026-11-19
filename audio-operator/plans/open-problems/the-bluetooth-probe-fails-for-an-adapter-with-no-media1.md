# The Bluetooth probe fails for an adapter with no `Media1`

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced the probe's result on
2026-11-19. Whether bluetoothd publishes such an adapter is not
known.

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
reads "the adapter advertises none of the 0 media profiles that
bluetoothd hosts", because `hostedProfiles` counts the profiles of
`Media1` interfaces and there are none.

The case in `endpoints_test.go` for an adapter with no `Media1`
expects `registered` false, so the tests pass with this behavior.

## The reproduction

The test called `endpointsFrom` on a managed-object tree with one
adapter that advertises the AudioSource profile and has no `Media1`
interface. It asserted the result for which the probe passes: no
adapter, or an adapter that is registered. It failed with:

    endpointsFrom = 1 adapters, registered false, so the probe fails and the kubelet restarts WirePlumber, which adds no Media1

The test is not in the tree, because a failing test cannot land on
`main`. The restart loop needs a machine whose bluetoothd publishes an
adapter without `Media1`, and the restart count of the WirePlumber
container on it. It is not known
when bluetoothd publishes an adapter that way: an adapter that is
still starting, an adapter that is powered off, or a bluetoothd built
without the media plugin are candidates.

## What a fix has to decide

Should an adapter with no `Media1` restart WirePlumber? One option
counts it as no adapter, so the probe passes. Another keeps the
failure, which needs a reason to expect that a restart helps. When it does not, a machine whose adapters all lack `Media1`
takes the branch for no adapter, which passes. When it does, the
probe needs a reason to expect a restart to help.
