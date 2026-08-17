# 02, An output for every connector

Proposed.

This plan answers the open problem "Routing is narrower than
inventory", and this document replaces it.

## The problem

The operator writes `weston.ini` once, at startup, with one `[output]`
section for each connector that has a monitor on it. A connector that
was dark at startup gets no section, so no app-id routes to it. The
operator publishes that connector's device with the
`display.liken.sh/no-output` taint, and a claim on it parks
`Unschedulable` for as long as the operator runs.

The only repair is a restart of the operator. The new pod starts a new
compositor, and every client on every screen loses its Wayland
connection. One new monitor ends every session in the house.

## What the compositor already does

Weston 14.0.2 handles a connector that changes at runtime, end to end.
These facts are read from the source, Debian's `weston 14.0.2-1`, not
measured here. The drill measures them.

* On a hotplug event, the DRM backend rescans the card's connectors
  and makes a head for a connector that now reports a monitor
  (`libweston/backend-drm/drm.c`, `udev_drm_event`).
* The frontend enables the new head and configures it from the
  `[output]` section whose `name=` matches the head
  (`frontend/main.c`, `drm_heads_changed`).
* The kiosk shell reads that section's `app-ids=` at the moment the
  output is created (`kiosk-shell/kiosk-shell.c`,
  `kiosk_shell_handle_output_created`).

Two facts keep this path from running on liken.

First, Weston parses its config once, at startup, and holds it in
memory. A section added to the file later is never read. So the
section for a dark connector has to exist before the monitor arrives.

Second, Weston subscribes to hotplug on the "udev" netlink group
(`drm.c`, `udev_monitor_new_from_netlink(b->udev, "udev")`). Only a
running udevd broadcasts on that group, and liken runs none. The
kernel broadcasts the same events itself, on the "kernel" netlink
group. That is the group this operator's own uevent listener already
reads, and it works. libudev parses the kernel group's raw format
natively whenever a monitor is created with the name `"kernel"`.

## The design

Three changes.

**A section for every connector.** The startup config gets one
`[output]` section per enumerated connector, dark or lit. Weston
enables only the heads that report a monitor, so a dark section is
inert until its monitor arrives. When it does, the hotplug path
configures the head from the section that was waiting for it.

**A preload shim.** A small C library exports
`udev_monitor_new_from_netlink`. When Weston asks for the "udev"
group, the shim calls the real libudev with "kernel" instead. The
operator sets `LD_PRELOAD` in the compositor's environment only, so
nothing else is touched. Nothing runs, nothing rebroadcasts, and no
event is synthesized. Weston listens the way every liken program
listens, and the shim exists only because Weston hard-codes the group
instead of offering a knob.

The event itself is one kernel datagram on netlink group 1: a `change`
action on the card's device, with `HOTPLUG=1` in its environment, and
a numeric `CONNECTOR=` id when the kernel can name the connector. The
DRM subsystem puts `HOTPLUG=1` in the broadcast itself; it is stored
nowhere in sysfs.

**The `no-output` taint retires.** Its whole reason was "this
connector has no routing until a restart". With routing complete from
startup, a dark connector needs only the honest taint,
`display.liken.sh/disconnected`, which is unchanged. The refusal in
`NodePrepareResources` retires with it.

## What unplug now costs

Delivering hotplug events changes unplug as well. Today Weston never
learns that a monitor left, so a cable reseated within the claim's
`tolerationSeconds` resumes the same session with no sign. With
events, Weston destroys the output when the connector disconnects, and
recreates it on replug. A bumped cable now costs that one session. It
can never cost more than the one screen whose cable moved, and the
drill records what the client on that screen observes.

## What was considered and set aside

* **`force-on=true` on every section.** Weston enables a forced head
  even when it is disconnected, so every connector would route from
  startup with no shim. But a dark connector has no EDID, so the
  config must guess a fixed modeline, and Weston never re-modes a
  live head. The source logs "Detected a monitor change ... not
  bothering to do anything about it." A guessed mode serves every
  monitor that is not that mode badly, forever.
* **libudev-zero.** A drop-in libudev for machines without udevd. Its
  monitor still needs a device manager to rebroadcast kernel events
  onto a netlink group, so it moves the problem without solving it.
* **Running udevd.** The heaviest possible answer to a one-argument
  problem, and liken's init already proves the kernel group alone is
  enough.
* **Patching Weston.** A one-line patch gets the same result as the
  shim, and costs owning a Weston build. The shim costs one small
  file in the image we already build.
* **A different compositor.** cage serves one app on one output by
  design. The wlroots family learns about hotplug through libudev the
  same way Weston does, so the same silence returns with a desktop's
  worth of config on top. Mutter wants logind and a session bus. The
  kiosk shell's config-file routing table is the interface an
  operator wants, and nothing else has it.
* **Minted app-ids.** Still set aside. Weston reads a section's
  `app-ids=` only when it creates the output, so minting a fresh
  app-id per allocation has no path into a running compositor. The
  open problem "The app-id is a guessable string" keeps that
  question.

## The drill

The drill runs on liken-1, whose DP-1 connector publishes dark today.
It must show four things.

1. **The event, byte for byte.** A pod with a listener on netlink
   group 1 captures the real datagram while a monitor is plugged in.
2. **A monitor arrives, nothing else moves.** With clients holding
   two lit screens, a pod parks on a claim for the dark connector.
   Plugging a monitor in must take the pod from `Unschedulable` to
   `Running` with no operator restart, and the two running clients
   must show zero restarts.
3. **A monitor leaves.** Unplugging it must evict only its own pod,
   on that claim's toleration, and the orphaned surface must not
   cover another client's screen. What the kiosk shell does with an
   orphaned surface is the one edge the source does not settle.
4. **A reseated cable.** Unplug and replug within the toleration,
   and record what the session does. That record becomes the
   documented behavior.
