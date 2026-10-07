# The observatory-operator design

`observatory-operator` runs an observatory's hardware through INDI.
`astrophotography-operator` runs imaging sessions on it, and a person
can drive it from KStars on a desktop instead. [Root plan
74](../../plans/74-astrophotography.md) holds the architecture and the
tests that chose it.

## The topology

Each INDI device runs in its own pod, with its own DRA claim, as
`socat TCP-LISTEN:7625,reuseaddr EXEC:<driver>,pipes`. Each
`Telescope` has one `indiserver` pod, which reaches each of its
devices through `indi-shim`, one symbolic link for each device. The
`Observatory` has one more server for the devices that no telescope
owns. A device joins or leaves a running server through `indiserver`'s
fifo, with no restart of the server ([plan
11](completed/11-devices-join-a-running-server.md)). The operator holds one INDI client for each server, and connects
each device and applies its settings when the device appears. PHD2 guides each
telescope from its own pod, as a client of the telescope's server. The
operator starts PHD2, connects it to the guide camera and the mount,
and reads its state from PHD2's event server. The holder calibrates and
guides.
Plan 03 tested this topology on a cluster.

The dome and the mounts run on different servers, and a driver reads
another device's state only through its own server. So the operator
relays the park states that the lock policies need between the
servers, and INDI's drivers enforce the locks ([plan
12](completed/12-the-dome-and-mount-locks-across-servers.md)).

## The resources

The resources are in the API group `observatory.liken.sh`. [Plan
06](completed/06-the-resources.md) gives the names, the reasons for them, and an
example.

```
Observatory          Dome, WeatherStation, policies across telescopes
 └─ Telescope        one Mount, one indiserver, one Guider
     ├─ OpticalTube  passive: aperture and focal length
     └─ OpticalTrain one light path; each names one OpticalTube
          Camera, FilterWheel, Focuser, Rotator, DustCap, FlatPanel
```

Each resource names its parent in its `spec`. Owner references are
only on the objects that the operator creates. The first device kinds
are the 14 that have a simulator in the `indi-simulators` image. The
CRDs are in `deploy/`, every kind is in the category `astro`, and the
package `observatory` holds the Go types. `examples/simulators.yaml` is
an observatory of simulators with a device of every kind.

A device resource is inventory, and the operator starts nothing for it.
A device's parent field is optional: a device with no parent is on the
shelf, and the operator creates no pod, `Service`, or `ResourceClaim`
for it.
A `Reservation` gives one holder the use of one `Telescope`: a
person's KStars in mode 1, or a `Session` of `astrophotography-operator`
in mode 2. Activation waits for the devices to be powered on, then
starts the pods, and starts the guider last. Deactivation stops the
guider, parks the mount, and warms the camera before it stops the pods
and reports that the devices are safe to power off.

## The images

`indi/` at the top of the repository builds the images: `indi`, with
`indiserver` and every core driver, `indi-simulators`, and one image
for each family of vendor drivers. A device names its driver, and the
operator resolves the image from a map that the `indi` build writes. A
device can name its own image instead. Plan 06 states what that image
must hold.

## The plans

| Plan | Subject |
|---|---|
| [01](completed/01-the-indi-base-image.md), [02](completed/02-driver-and-simulator-images.md), [03](completed/03-the-topology-by-hand.md) | Built: the images, and the topology from static manifests |
| [04](completed/04-the-indi-client-in-go.md) | Built: the INDI client in Go, the package `indi` |
| [05](05-the-property-schema.md) | Typed fields generated from the simulators and INDI's documentation |
| [06](completed/06-the-resources.md) | Built: the resources, as CRDs and the package `observatory` |
| [07](completed/07-the-operator-runs-the-topology.md) | Built and drilled on a test cluster: the operator creates the topology from the resources |
| [08](completed/08-the-reconciler.md) | Built and drilled: the reconciler, in the operator; the lock policies between the dome and the mounts came in plan 12 |
| [09](completed/09-the-guider.md) | Built and drilled on a test cluster: the guider, PHD2 in its own pod beside a headless weston |
| [10](10-access-from-a-desktop.md) | KStars on a desktop, which completes mode 1 |
| [11](completed/11-devices-join-a-running-server.md) | Built: a device joins or leaves a running server, with no restart of the server; drilled on the test cluster |
| [12](completed/12-the-dome-and-mount-locks-across-servers.md) | Built: the operator relays the park states between the servers, so the drivers enforce the dome and mount locks; drilled on the test cluster |
| [13](completed/13-procedures-on-conditions.md) | Built: each resource states its own procedures, triggered by conditions; the policy flags and the hard-coded `Prepare` and `Secure` go; drilled twice on the test cluster |
| [14](completed/14-named-states-and-clean-deletes.md) | Built: readings name one state instead of a pair of booleans, and a finalizer runs a deleted resource's deactivation; the drill on the test cluster has not run |
