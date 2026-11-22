# 46, A receiver that takes its sound from the panel

Proposed on 2026-11-21, as a stub. Nothing is built.

## The problem

A `Player` reports one `Receiver` in `status.receiver`, and its CRD
describes that field as the `Receiver` that the `Player`'s video goes
through. That is true when the machine's HDMI cable goes into an A/V
receiver and the receiver's output goes to the TV. It is false in a
room where the machine's HDMI cable goes to the TV, and the TV sends
its sound to an amplifier over an optical cable. No picture goes
through that amplifier, and the sound reaches it after the panel.

The operator finds a `Player`'s `Receiver` from the machine that draws
the screen and the monitor id of the screen, which an entry of the
`Receiver`'s `spec.inputs` names. Both rooms match that way, so
`status.receiver` has the same fields in both: `name`, `input`, and
`reachable`. No field of the `Player` or of the `Receiver` says
whether the receiver is before the panel or after it.

So a reader of the status cannot tell the two rooms apart, and neither
can a program. `transect` draws the signal path of a `Player` from
`media-operator/transect/kinds.yaml`, with one wire from the machine to
the display through the receiver that carries picture and sound. On
2026-11-21 it drew that path for a room of the second kind, with the
picture going through an amplifier that has no video input.
[Plan 43](43-the-level-follows-the-sound.md) is about the same fact
for another reader: which device carries the unit's sound.

## The questions that the design answers

- Which object states where the receiver is: an entry of the
  `Receiver`'s `spec.inputs`, which a person writes, or
  `equipment-operator`, from what the driver can do? A WiiM amplifier
  passes no video.
- Does `status.receiver` keep its meaning, with a second field for a
  receiver that takes its sound from the panel, or does it gain a
  field that says which it is? With a second field, the knowledge of a
  `Player` has a role for each, and `transect` needs no rule that
  depends on a value.
- What the power and the input of such a receiver follow, and whether
  plan 43's rule for the level already covers it.
