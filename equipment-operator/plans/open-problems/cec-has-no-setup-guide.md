# CEC has no setup guide

The operator's manual has an install guide and an install skill, and
nothing that takes a person from a new USB-CEC adapter to a TV that
wakes. Plan 09's phase 5 listed what that guide covers: the
`Machine`'s `spec.modules` and `spec.serio` (`liken` plan 70), the
`Display`'s physical address (display-operator plan 23), a `CECBus`
from `Listen` to `Control`, the `Television`, and a `Remote` on the
adapter's input device (media-operator plan 35). It also states what
the drills taught: put the adapter on a spare input with only its own
cable, expect many HDMI cables to leave out the CEC wire, and expect
another source to take the input after a wake. It links the kernel's
CEC admin guide, the Pulse-Eight pages, and `cec-ctl` for a check by
hand.

The guide goes in `docs/content/docs/guides/` with a matching skill in
`skills/`. Plan 73 adds menu and deck control over CEC, so the guide
may wait for it and cover both.
