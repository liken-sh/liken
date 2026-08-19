# A2DP does not survive a Bluetooth pod restart

When the Bluetooth pod restarts, its dbus-daemon exits and every
connection to the bus dies. Plan 04's drill measured what happens in
this pod on 2026-08-19:

* The operator container's BlueZ signal channel closes, the process
  exits, and the kubelet restarts it. It reconnects within 3
  seconds and the slice publishes on.
* WirePlumber notices nothing. Its connection is dead, it logs
  nothing, it keeps running, and it never re-registers the media
  endpoints. From that moment the radio advertises no A2DP, no
  speaker can pair or connect, and every pod and container reports
  Ready.

The repair today is a manual delete of the audio pod, which also
interrupts the card's audio on that machine. liken's milestone 60
accepted a restart coupling as a cost, but it assumed something
would restart. Nothing does.

A fix must first make something notice. The operator already
notices, so the shape to decide is what it does with that fact:

* End the whole pod instead of only itself. The operator holds no
  grant to delete its own pod, and its exit restarts only its own
  container.
* Make WirePlumber's health visible: a probe that fails when the
  bus connection is dead would let the kubelet restart the
  WirePlumber container alone. The image has no shell, so the probe
  would need a binary of its own, and WirePlumber exposes no such
  check today.
* Publish the fact: taint the speaker devices while the endpoints
  are unregistered, so the API stops claiming a dead path works.
  This is honest but repairs nothing.

Milestone 60's drill 7 asks whether a WirePlumber restart alone
repairs the path, with PipeWire and the card's sinks left up. That
measurement decides which of these shapes is worth building.
