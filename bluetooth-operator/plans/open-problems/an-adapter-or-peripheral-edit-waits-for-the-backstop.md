# An Adapter or Peripheral edit waits for the backstop

Open problem. The operator watches `PairingRequest`s, so a new request
or an approval acts at once. It does not watch `Adapter`s or
`Peripheral`s. An edit to an `Adapter`'s `spec.alias`, an edit to a
`Peripheral`'s `spec`, and the deletion of a `Peripheral`, which starts
an unpair, take effect on the next pass. With nothing else to wake the
loop, that pass is the backstop tick, at most 60 seconds later
(`backstopInterval` in `main.go`).

## Why it stays for now

A watch on each kind would make those edits act at once, but it would
not remove the tick. The tick also covers bond files that bluetoothd
writes with no D-Bus signal to announce them (`bondstore.go`), and no
event covers those. So the watches would add two long-lived streams per
node and their restart handling, and save no cost. The only gain is
the reaction time of an edit that a person makes rarely.

## What would change the answer

A workflow that edits `Adapter`s or `Peripheral`s often, or one where a
person waits on an unpair to finish, such as moving a controller from
one node to another. Then the operator would list and watch both kinds
from the list's version, with the organization's watch-loop guards,
and wake the loop on each change.
