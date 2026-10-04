# The `Receiver` has no `cec:` block

A receiver on a CEC bus is the bus's audio system, and only CEC can
put it in System Audio Mode: the TV sends its sound to the receiver
and passes its volume keys to it. Plan 09's phase 4 designed a `cec:`
block on the `Receiver` that names the bus, so one `Receiver` holds
both the network path and the CEC path to one receiver. The network
block owns power, input, volume, and mute, and the `cec:` block owns
System Audio Mode. A `Receiver` with only `cec:` would also take power,
volume, and mute over CEC. `status.cec` would report the receiver's
address, power, and System Audio Mode.

None of that is built. The node workload decodes and logs System Audio
Mode messages that it hears on the bus, but it sends none. The network
drivers now cover power, input, and volume for a Denon or a WiiM, so
the open question is whether a room needs System Audio Mode from the
cluster, or a receiver with no network interface, before this is worth
building. The design is in
[plan 09](../completed/09-cec.md#the-receivers-cec-block).
