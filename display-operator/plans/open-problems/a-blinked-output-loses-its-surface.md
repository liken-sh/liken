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

The `display.liken.sh/disconnected` taint now drives an automatic
repair for a consumer that resumes on eviction. The operator taints
a dark output `NoExecute`. A consumer that tolerates the taint rides
the blink and strands its picture. A consumer that does not tolerate
it is evicted, and the media-operator's playback pod, which dropped
that toleration on 2026-08-21, recreates and resumes the film at its
place on the output that returned. For that consumer the blink heals
by itself in about a second.

The stranding itself is unchanged. kiosk-shell still evacuates the
surface and never maps it back, so a consumer that stays alive
through the blink still shows its picture on the wrong panel. The
automatic repair is an eviction and a restart, not a surface that
follows its output home.

The shapes that would keep the film alive through the blink, none
built:

* Restart nothing, remap instead. A compositor-side hook that
  records each surface's intended output and re-places it when
  that output returns. kiosk-shell has no such hook today, so this
  is an upstream conversation or a patch this repository carries.
* Let the consumer ask again. A client that re-asserts its output
  on every configure event would follow the output home. That
  moves the fix into every consumer image, which is the wrong
  side of the contract.
