An `Adapter` is one Bluetooth radio. The operator creates the
object for the adapter its pod claimed and names it for the radio's
address, lowercase with dashes. It is the root of the pairing
records: every [Peripheral](/docs/reference/peripherals/) bonded
with the radio belongs to it, so deleting an `Adapter` collects
every bond and every bond `Secret` with it. The operator refuses
that deletion while the radio is present; unplug the radio to let
it through. None of these objects names a machine, so a dongle
moved to another machine keeps its `Peripherals` and their stored
keys.
[Pair a controller](/docs/guides/pair-a-controller/) starts by
reading this object's name.

`spec.privacy` turns on Low Energy privacy for the radio.
[Privacy](/docs/concepts/privacy/) explains what it hides and what it
costs. `bluetoothd` reads the setting only when it starts, so when
`spec.privacy` differs from the value the running `bluetoothd` started
with, the operator stores the bonds, posts a `PrivacyChanged` `Event`
on the `Adapter` that names both values, and deletes its own pod. The
`DaemonSet` creates a new pod, which starts `bluetoothd` with the new
value. Every connected controller drops for the few seconds the new pod
takes to start, and reconnects as it does after any restart of the
pod. An empty field is `off`.

`status.privacy` and the `PRIVACY` column of `kubectl get adapters`
show the value the running `bluetoothd` started with:

    $ kubectl get adapters
    NAME                ALIAS   ADDRESS             NODE      POWERED   PRIVACY   AGE
    04-4a-69-66-92-27           04:4A:69:66:92:27   liken-1   true      device    1m

With privacy on, the operator keeps the radio's identity resolving key
in the `Secret` `bluetooth-identity-<adapter>` in the operator's
namespace. The `Adapter` owns it, so deleting the `Adapter` collects
it. The operator keeps it when privacy goes off again.
