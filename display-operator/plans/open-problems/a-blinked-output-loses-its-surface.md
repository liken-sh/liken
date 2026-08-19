# A blinked output loses its surface

When an output disappears and returns, the surface that was
fullscreen on it does not come back. The movie drill on 2026-08-19
measured it: a monitor's HDMI link dropped for a moment while
cables moved, Weston removed the output and re-added it, and the
movie that had been fullscreen there reappeared on the other
monitor and stayed.

The move is kiosk-shell's own policy. When an output goes away,
the shell evacuates its surfaces to a surviving output so no
client is left presenting to nothing. When the output returns,
nothing maps the surface back: the shell keeps no record of where
a surface used to live, and the client asked for its output once,
at startup, through `--wayland-app-id` placement.

The repair today is to recreate the consumer pod. A fresh surface
asks for its output again and lands correctly. That is a poor
answer for a machine where monitors sleep, switch inputs, or share
a flaky cable: every blink strands a workload's picture on the
wrong panel until something recreates the pod.

The shapes worth weighing, none built:

* Restart nothing, remap instead. A compositor-side hook that
  records each surface's intended output and re-places it when
  that output returns. kiosk-shell has no such hook today, so this
  is an upstream conversation or a patch this repository carries.
* Make the operator notice. It already watches the connectors, so
  it could taint the output's device while the surface is
  elsewhere. Honest, like the audio operator's dead-endpoint
  taint idea, and it repairs nothing.
* Let the consumer ask again. A client that re-asserts its output
  on every configure event would follow the output home. That
  moves the fix into every consumer image, which is the wrong
  side of the contract.
