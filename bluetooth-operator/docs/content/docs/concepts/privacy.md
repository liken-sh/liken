---
title: Privacy
weight: 20
---

# Privacy

Low Energy privacy keeps a device that never paired with your radio
from recognizing it each time it appears. With privacy off, the radio
advertises and connects from one fixed address, so anybody in range can
tell that the same radio is back. With privacy on, the radio uses a
resolvable private address that it changes every few minutes. Privacy is
off unless you set it, with `spec.privacy` on the radio's
[`Adapter`](/docs/reference/adapters/#spec--privacy).

## How a paired device still finds the radio

The private address comes from the radio's identity resolving key
(IRK). Two devices exchange their IRKs when they pair, so a bonded
controller resolves each new address back to the radio, and a device
that never paired with it cannot. The setting belongs to the radio, so a
`PairingRequest` or a `Peripheral` has no field of its own for it.
`bluetoothd` applies the same value to every bonded device it lets
reconnect.

Privacy applies to Low Energy links only. A classic link, such as a
DualSense's, always uses the radio's public address.

## What a change costs

`bluetoothd` reads the setting once, when it starts, and the kernel
accepts the privacy command only while the radio is powered off. So the
operator applies a change by deleting its own pod, after it stores the
bonds, and the new pod starts `bluetoothd` with the new value. Every
connected controller drops for the few seconds the new pod takes to
start, and reconnects as it does after any restart of the pod. The
`Adapter` reference describes the `PrivacyChanged` `Event` and the
status the restart leaves.

## The identity key

`bluetoothd` writes the radio's IRK the first time it starts with
privacy on. The operator stores the key in the `Secret`
`bluetooth-identity-<adapter>`, and the next pod writes it back before
`bluetoothd` starts. So a new pod, a reinstall, or a radio moved to
another machine keeps the identity that its paired devices know. The
operator keeps the `Secret` when you turn privacy off, so a radio that
turns privacy on again presents the same identity.

## Devices that paired while privacy was off

A device that paired while privacy was off may not hold the radio's
IRK. Such a device cannot resolve the radio's private address after you
turn privacy on, and a Low Energy controller that accepts connections
only from its bonded radio may refuse to connect until you pair it
again. This has not been measured on real controllers yet. If a Low
Energy controller does not reconnect after you turn privacy on, pair it
again.
