# observatory-operator

`observatory-operator` runs an observatory's equipment on a
[`liken`](https://liken.sh/) cluster. You describe your mount,
cameras, focusers, dome, and other equipment as resources of the API
group `observatory.liken.sh`. To use a telescope, you reserve it with
a `Reservation`. While the reservation is active, the operator runs
each of the telescope's INDI devices in its own pod, and serves all
of them on one INDI server. It connects and configures each device in
a fixed order, and starts PHD2 for the telescope's guide camera and
mount. You drive the telescope from KStars and Ekos, as you would
with any INDI server. When the reservation ends, the operator parks
and warms the equipment the way you declared. Then it reports that
the equipment is safe to power off.

The operator manages the device pods, the INDI servers, and the
guider's pod. It also runs the procedures that each resource declares
for the start and the end of a night, such as opening a dome or
cooling a camera. It does not take pictures, plate-solve, or guide on
its own, and it records nothing about targets or nights. That work
belongs to the reservation's holder, the program that uses the
telescope through the INDI server and PHD2's event server.

The OS publishes each machine's USB devices. Each device resource
names a claim, and the device's pod gets its USB device through that
claim. The drivers come from the images of [`indi/`](../indi/). The
guider runs PHD2 from the `indi-phd2` image beside a headless
`weston`. Some cameras, such as many QHY cameras, power up with no
firmware. They need `firmware-operator`, and `deploy/firmware/`
contains a `Firmware` for each such QHY chip.

The operator is useful on its own, with KStars as the holder.
[`astrophotography-operator`](../astrophotography-operator/) is
another holder: it reserves a telescope to image the targets that you
declare.

## The kinds

- `Observatory` is the site: its location, and the devices that its
  telescopes share, such as a dome or a weather station.
- `Telescope` is one mount with its own INDI server. `OpticalTube`
  records a tube's aperture and focal length. `OpticalTrain` is one
  light path through a tube, with the devices on it.
- The device kinds, such as `Mount`, `Camera`, `Focuser`,
  `FilterWheel`, `Dome`, `WeatherStation`, and `Switch`, each
  represent one INDI interface. A device names its driver and its
  parent. The operator starts nothing for a device until a
  reservation needs it.
- `Guider` runs PHD2 for a telescope, with the camera of one
  `OpticalTrain`.
- `Reservation` gives one holder the use of one `Telescope` from
  `spec.start` to `spec.end`.
- `Horizon` is a saved horizon profile. A `Telescope` refers to it by
  name.

`kubectl get astro` lists resources of every kind.

## The Go packages

The Go module `github.com/liken-sh/liken/observatory-operator`
contains the operator and the packages below.
`astrophotography-operator` imports `observatory`, `indi`, and
`phd2`.

- [`observatory/`](observatory/) has the Go types of the kinds.
- [`indi/`](indi/) is a client of INDI protocol version 1.7, in Go
  with no cgo. `indi/inditest` is a fake `indiserver` for tests.
- [`phd2/`](phd2/) is a client of PHD2's event server. It reads
  PHD2's state and guide steps. It sends the requests that connect,
  loop, guide, dither, and stop. `phd2/phd2test` is a fake event
  server for tests.
- [`drivers/`](drivers/) maps each INDI driver to the `indi` image
  that contains it.

The module also builds the `kubectl liken observatory` plugin from
[`cli/`](cli/). You use it to confirm that you set a telescope up for
the night.

## An example

[`examples/simulators.yaml`](examples/simulators.yaml) is an
observatory of INDI simulators, with a device of every kind. It ends
with this reservation. The reservation starts the `east` telescope at
once, and the telescope keeps running until you delete the
reservation:

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Reservation
metadata:
  name: east-tonight
spec:
  telescope: east
  holder: desktop
```

The manual is at [liken.sh/observatory](https://liken.sh/observatory/),
and [`plans/00-design.md`](plans/00-design.md) is the design.
