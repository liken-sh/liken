# bluetooth-operator

`bluetooth-operator` pairs Bluetooth devices through the Kubernetes
API. It gives each paired input device, such as a game controller or
a remote, to exactly one pod. The pod claims the device by its MAC
address. It gets that device's input nodes, and no other input device
on the machine. You pair with `kubectl`, so RBAC controls who can
pair, and nobody needs a shell on a node or in a pod.

The operator runs `bluetoothd` for the machine's radio. When you
create a `PairingRequest`, the operator opens a pairing window and
pairs the device whose address you approve. It stores the keys of
each pairing, called a bond, in a `Secret`. So a device reconnects
after a pod restart, an upgrade, or a reboot. The operator publishes
every paired device, whether it's connected or not. So a pod that
claims a switched-off controller starts when you turn the controller
on. Through this operator, a pod can claim only an input device. A
paired speaker plays through `audio-operator`, and this operator runs
no sound server. The operator serves one radio on each machine.

`bluetooth-operator` is one of `liken`'s
[extension operators for claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/).
It claims the radio that `liken` publishes, through the same public
interfaces that any driver uses, and a cluster can run without it.
It also publishes each radio's media bus, which is a D-Bus connection
to that radio's `bluetoothd`. `audio-operator` claims the media bus to
play sound to paired speakers. A `Remote` of `media-operator` can
claim a paired controller, and `media-operator` reads the
controller's link and battery from its `Peripheral`.

## The kinds

- An `Adapter` is one Bluetooth radio. The operator creates it for the
  radio that its pod claimed. Its `spec` sets the name that the radio
  broadcasts, Low Energy privacy, and the `btmon` trace of the radio's
  link.
- A `PairingRequest` opens one pairing window on one adapter. It
  reports the devices that the radio finds, and pairs the one whose
  address you write into `spec.device`.
- A `Peripheral` is one paired device. It owns the `Secret` that
  stores the device's keys. Its `status` reports the link and the
  battery. To unpair the device, delete its `Peripheral`.

This `PairingRequest` opens a window of 180 seconds on the radio
`00-11-22-33-44-55`. To pair a device that the radio finds, write its
address into `spec.device`:

```yaml
apiVersion: bluetooth.liken.sh/v1alpha1
kind: PairingRequest
metadata:
  name: new-gamepad
  namespace: liken-system
spec:
  adapter: 00-11-22-33-44-55
  windowSeconds: 180
```

The manual is at [liken.sh/bluetooth](https://liken.sh/bluetooth/).
It has the install guide, the guides, and a reference for each kind.
The design documents and the open problems are in [`plans/`](plans/).

MIT. See [LICENSE](LICENSE).
