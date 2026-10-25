# A TV behind a receiver answers no DDC/CI

`display-operator` turns a panel off with a DDC/CI write. A TV that a
machine reaches through an A/V receiver does not answer DDC/CI, so its
`Display` reports `RESPONSIVE` False, and an override is skipped with
no condition, Event, or log line. On such a `Display`, a `Player`'s
`off` panel desire draws a black picture, and the HDMI signal goes on.

A `Player` that is off still turns its TV off, over HDMI-CEC through
`equipment-operator` (root plan 84). The gap is the soft off of the
idle timers, which leaves the TV on and lit with a black picture, and
the skipped override, which reports nothing.
