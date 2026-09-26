# Invalidate device grants after hardware removal

Open bug, medium priority. A prepared claim keeps its device paths after
the allocated hardware disappears. If the kernel reuses a path for
another device, a later container start can receive that device under
the old claim.

## Failure path

`refreshCDISpec` in [cdi.go](../../machine-operator/cdi.go) resolves each
prepared device against current sysfs data. When `resolveAllocated`
fails, it leaves the existing `ContainerEdits.DeviceNodes` unchanged.
For example, the spec can keep `/dev/sda` after the allocated USB
address no longer exists.

For these path-only CDI entries, the runtime resolves the host device
at container creation. If `/dev/sda` now belongs to hardware at a
different address, the old claim still grants that path. The claim names
one device, and the container receives another.

## Evidence and existing behavior

A temporary fixture prepared a claim for USB address `2-1:1.0`, with
`/dev/sda` as its device node. The fixture then moved the fake hardware
to `2-2:1.0` and kept `/dev/sda`. After refresh, the old claim was
unchanged. This tests the stale CDI state only. No physical hot-plug or
containerd test was performed.

`TestRefreshKeepsTheNodesOfHardwareThatLeft` in
[cdi_test.go](../../machine-operator/cdi_test.go) explicitly expects the
old paths to stay. Its reason is that an empty edit list would start a
container without the requested device and without an error. That
outcome is also wrong, but keeping the stale path is unsafe.

Driver detachment is different from removal. When the hardware remains
present and a userspace driver detaches its kernel driver, its usbfs
node can remain valid. `TestRefreshDeliversTheBusNodeAloneAfterADriverDetach`
checks the supported rewrite to that node.

## Proposed safeguard

Invalidate a prepared device that no longer resolves. A container start
that requires its CDI ID then fails. It does not inject a stale path,
and it does not silently omit the device. Keep the driver-detach path
for hardware that is still present.

The repair must also restore a valid CDI entry when the allocated
hardware returns. The kubelet may reuse its prepare result. So if the
repair deletes the only local record of a prepared claim, and nothing
can rebuild it, the device may not recover.

Changing a CDI spec does not revoke the open device descriptors of a
container that is already running. This safeguard applies to later
container creation. Revoking access from running containers is a
separate problem.

## Remedy scope

The fix is a focused safeguard for stale paths. Broader recovery choices
are separate decisions. Refusing access to an unrelated device keeps the
existing isolation between claims.

The [device reference](../../docs/content/docs/reference/devices.md)
defines device names by bus address. Replacing a failed dongle in the
same port keeps that name. The fix should not silently replace this
port-based identity with serial-number identity, follow hardware to a
new port, or promise transparent recovery of running containers. Those
would change which hardware a claim represents and need design decisions.

## Tests needed

Test disappearance followed by device-path reuse at a different address,
then verify that container creation fails without granting the replacement.
Also test return at the original address, operator restart while the device
is absent, and driver detach without removal. Use a runtime-level test to
verify that invalidation fails the start rather than dropping the edits.
