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
