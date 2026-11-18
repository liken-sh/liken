An `Adapter` is one Bluetooth radio. The operator creates it for the
adapter that its pod claimed, and names it for the radio's address in
lowercase with dashes. The `Adapter` owns every
[Peripheral](/docs/reference/peripherals/) bonded with the radio, so
deleting an `Adapter` also deletes its `Peripherals` and their bond
`Secrets`. While the radio is present, the operator keeps its
finalizer on a deleted `Adapter` and writes the reason to
`status.deletionRefused`. The deletion completes after you unplug the
radio. The ownership does not depend on a machine, so a dongle that
you move to another machine keeps its `Peripherals` and their stored
keys.
[Pair a controller](/docs/guides/pair-a-controller/) starts by
reading this object's name.

`spec.privacy` turns on Low Energy privacy for the radio.
[Privacy](/docs/concepts/privacy/) explains what it hides and what it
costs. `bluetoothd` reads the setting only when it starts. When
`spec.privacy` differs from the value that the running `bluetoothd`
started with, the operator stores the bonds, posts a `PrivacyChanged`
`Event` on the `Adapter` that gives both values, and deletes its own
pod. The `DaemonSet` creates a new pod, which starts `bluetoothd` with
the new value. Every connected controller disconnects, and reconnects
as it does after any restart of the pod. An empty field means `off`.

`status.privacy` and the `PRIVACY` column of `kubectl get adapters`
show the value that the running `bluetoothd` started with:

    $ kubectl get adapters
    NAME                ALIAS   ADDRESS             NODE      POWERED   PRIVACY   BTMON   AGE
    04-4a-69-66-92-27           04:4A:69:66:92:27   liken-1   true      device    false   1m

With privacy on, the operator stores the radio's identity resolving
key in the `Secret` `bluetooth-identity-<adapter>` in the operator's
namespace. The `Adapter` owns the `Secret`, so deleting the `Adapter`
deletes it. The operator does not delete the `Secret` when you turn
privacy off.

`spec.btmon: true` turns on a trace of the radio in the pod's `btmon`
container. The container logs each packet on the radio's HCI link
and management channel, so the trace of a stalled controller is still
there after the pod restarts. The trace is off by default, because
`btmon` prints the link keys, the long term keys, and the radio's
identity key in plain text, and anyone who can read the pod's logs
can read them. Turn the trace on while you diagnose a fault, and turn
it off afterward:

    kubectl patch adapter 04-4a-69-66-92-27 --type merge -p '{"spec":{"btmon":true}}'

When `spec.btmon` differs from the value that the `btmon` container
reads, the operator writes the new value for the container and posts
a `BtmonChanged` `Event` on the `Adapter`. The container starts or
stops `btmon` within seconds. The pod does not restart, so no
controller disconnects. An empty field means `false`. `status.btmon`
and the `BTMON` column show the value that the container reads.
