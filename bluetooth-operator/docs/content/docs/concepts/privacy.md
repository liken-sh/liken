---
title: Privacy
weight: 20
---

# Privacy

Low Energy privacy keeps devices that never paired with your radio from
recognizing it each time it appears. With privacy off, the radio
advertises and connects from one fixed address, so anyone in range can
tell that the same radio is back. With privacy on, the radio uses a
resolvable private address that changes periodically. Privacy is off
until you set `spec.privacy` on the radio's
[`Adapter`](/docs/reference/adapters/#spec--privacy).

## How a paired device still finds the radio

The radio makes its private addresses from its identity resolving key
(IRK). Two devices exchange their IRKs when they pair, so a bonded
controller can resolve each new address to the radio, and a device
that never paired with it cannot. Privacy is a setting of the whole
radio and applies to every device it connects to, so a
`PairingRequest` or a `Peripheral` has no field for it.

Privacy applies to Low Energy links only. A classic link, such as a
DualSense's, always uses the radio's public address.

## Changing the setting

`bluetoothd` reads the setting only when it starts, and the kernel
accepts the privacy setting only while the radio is powered off. So
the operator applies a change by deleting its own pod, after it has
stored every bond in its `Secret`. The new pod starts `bluetoothd` with
the new value. Every connected controller disconnects while the new
pod starts, which takes a few seconds, and reconnects as it does after
any restart of the pod. The [`Adapter` reference](/docs/reference/adapters/)
describes the `PrivacyChanged` `Event` and the status after the
restart.

## The identity key

The first time a pod starts with privacy on and no stored IRK for the
radio, the pod's `bondfetch` init container writes a new random IRK
before `bluetoothd` starts. The operator copies the key into the
`Secret` `bluetooth-identity-<adapter>`, and each later pod writes it
back from the `Secret` before `bluetoothd` starts. So after a new pod,
a reinstall, or a move of the radio to another machine, the radio
keeps the identity that its paired devices have stored. The operator keeps
the `Secret` when you turn privacy off, so the radio uses the same
identity when you turn privacy on again.

## Devices that paired while privacy was off

A device that paired while privacy was off may not have the radio's
IRK, and then it cannot resolve the radio's private address after you
turn privacy on. A Low Energy controller that accepts connections only
from its bonded radio may refuse to connect until you pair it again.
A remote that wakes and advertises is different, because the radio
opens the link, and the remote does not need to resolve the radio's
address. An X6 remote that paired while privacy was off reconnected
without pairing again in a test on 2026-10-07. If a Low Energy
controller does not reconnect after you turn privacy on, pair it
again.
