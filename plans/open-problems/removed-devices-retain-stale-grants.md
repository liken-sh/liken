# Invalidate device grants after hardware removal

Open bug. Review priority: medium. A prepared claim retains its device
paths after the allocated hardware disappears. If the kernel reuses a
path for another device, a later container start can receive that device
under the old claim.

## Failure path

`refreshCDISpec` in [cdi.go](../../machine-operator/cdi.go) resolves each
prepared device against current sysfs data. When `resolveAllocated`
fails, it leaves the existing `ContainerEdits.DeviceNodes` unchanged.
For example, the spec can keep `/dev/sda` after the allocated USB
address no longer exists.

For these path-only CDI entries, the runtime resolves the host device
at container creation. If `/dev/sda` now belongs to hardware at a
different address, the old claim still grants that path. Claim identity
and the device injected into the container no longer agree.

## Evidence and existing behavior

A temporary fixture prepared a claim for USB address `2-1:1.0`, with
`/dev/sda` as its device node. Moving the fake hardware to `2-2:1.0`
while retaining `/dev/sda` left the old claim unchanged after refresh.
This verifies the stale CDI state; no physical hot-plug or containerd
test was performed.

`TestRefreshKeepsTheNodesOfHardwareThatLeft` in
[cdi_test.go](../../machine-operator/cdi_test.go) explicitly expects this
preservation. Its rationale is that an empty edit list would start a
container without the requested device and without an error. That is
also an incorrect outcome, but retaining the stale path is unsafe.

Driver detachment is different from removal. When the hardware remains
present and a userspace driver detaches its kernel driver, its usbfs
node can remain valid. `TestRefreshDeliversTheBusNodeAloneAfterADriverDetach`
checks the supported rewrite to that node.

## Proposed safeguard

Invalidate a prepared device that no longer resolves, so a container
start requiring its CDI ID fails rather than injecting a stale path or
silently omitting the device. Preserve the driver-detach path for hardware
that is still present.

The repair must also restore a valid CDI entry when the allocated
hardware returns. Deleting the only local record of a prepared claim
without a reconstruction path could prevent that recovery, since the
kubelet may reuse its prepare result.

Changing a CDI spec does not revoke an already-running container's open
device descriptors. This safeguard concerns subsequent container creation;
revocation of running access is a separate problem.

## Remedy scope

**Focused safeguards for stale paths, with broader recovery choices kept
separate.** Refusing access to an unrelated device preserves the existing
isolation contract.

The [device reference](../../docs/content/docs/reference/devices.md)
defines device names by bus address. Replacing a failed dongle in the
same port preserves that name. The fix should not silently replace this
port-based identity with serial-number identity, follow hardware to a
new port, or promise transparent recovery of running containers. Those
would change which hardware a claim represents and need design decisions.

## Tests needed

Test disappearance followed by device-path reuse at a different address,
then verify that container creation fails without granting the replacement.
Also test return at the original address, operator restart while the device
is absent, and driver detach without removal. Use a runtime-level test to
verify that invalidation fails the start rather than dropping the edits.
