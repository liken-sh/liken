# Claims do not follow a device to a new port

Open design problem. A claim that selects a USB device by its `serial`
attribute stays allocated to the device's old port after someone moves
the cable to a different port. The workload then fails, and nothing in
the cluster repairs it. A person must delete the claim and the pod.

## What happens

The [device reference](../../docs/content/docs/reference/devices.md)
names each device by its bus address. For USB, the address is the port
path, so a UPS whose interface address is `1-4:1.0` is `usb-1-4-1-0`.
The reference says that a claim which must select one physical unit
selects on `serial`.

A typical claim for a UPS:

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata:
  name: ups
spec:
  devices:
    requests:
      - name: ups
        exactly:
          deviceClassName: cyberpower-ups
          selectors:
            - cel:
                expression: |
                  device.attributes["liken.sh"].serial == "PY5LP0000000"
```

The scheduler matches the selector once, then writes the device name
into the claim's status:

```yaml
status:
  allocation:
    devices:
      results:
        - device: usb-1-4-1-0
          driver: liken.sh
          pool: node-a
          request: ups
```

Someone moves the UPS cable to another port on the same machine. The
node's `ResourceSlice` removes `usb-1-4-1-0` and publishes
`usb-1-1-1-0`, with the same `serial`. The claim's allocation does not
change. Kubernetes allocates a claim once and does not allocate it again
while a pod reserves it.

The pod runs Network UPS Tools. Its driver finds no UPS in the container
and exits:

```
Network UPS Tools 2.8.5 release - Generic HID driver 0.71
USB communication driver (libusb 1.0) 0.53
No matching HID UPS found
Driver failed to start (exit status=1)
```

The liveness probe fails, and the kubelet restarts the container in the
same pod, with the same prepared claim. The scheduler never evaluates
the pod again, so the pod stays in `CrashLoopBackOff`. The
[stale grants problem](removed-devices-retain-stale-grants.md) describes
what the CDI spec gives the restarted container in this state.

## The manual repair

1. Delete the claim. The delete waits for the claim's finalizer, because
   the pod still reserves the claim.
2. Delete the pod. The claim is then removed.
3. Create the claim again, for example with a GitOps reconcile. The
   scheduler allocates the new claim to `usb-1-1-1-0` by its serial,
   and the new pod starts.

The same failure occurred with a Zigbee adapter that a claim selected
by serial. The same repair fixed it.

## Port names and serial selectors

The reference chose the port as the device name on purpose. A claim on
"whatever adapter is in this port" keeps its name when someone replaces
a failed adapter with an identical one. That case needs the port name.

A claim that selects on `serial` states a different intent: this unit,
in any port. The allocation records the port, so it does not follow
that intent after a move. The selector continues to match the unit.
Only the allocation is out of date.

## Candidate directions

None of these is a selected design.

- **Name a device by its serial when it has one.** The allocation then
  continues to name the moved unit. The CDI spec must then give the new
  device node, and the kubelet must deliver it. It is not known if the
  kubelet prepares a claim again when a container restarts, or only when
  a pod starts. This also changes a published device name, so a claim
  that means "this port" would stop following a replacement unit.
- **Taint a device that leaves the slice.** Kubernetes has DRA device
  taints with a `NoExecute` effect, which evict a pod from a device. The
  feature is alpha. An eviction does not allocate a standalone claim
  again, so the workload would also need a `ResourceClaimTemplate`, which
  creates a new claim for each pod.
- **Document the repair.** The reference and the troubleshooting guide
  could tell a person that a port move needs the claim deleted. This
  removes no failure, but a person finds the repair without
  investigation.

## Relation to stale grants

The [stale grants problem](removed-devices-retain-stale-grants.md)
asks that a prepared device which no longer resolves fail the container
start. It excludes a change to serial identity and following hardware
to a new port, because those change what a claim represents. This
problem is that decision. The safeguard in the stale grants problem
makes the failure clear. It does not make a moved device work again.

## Verification needed

Move a claimed USB device to another port on the same machine, with a
claim that selects on `serial`. Verify that the workload runs again
with no person involved. Also move a device to a port on a different
machine, replace a unit in the same port under a claim that does not
select on `serial`, and unplug a device with no return.
