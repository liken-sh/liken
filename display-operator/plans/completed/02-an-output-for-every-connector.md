# 02, An output for every connector

Built, and drilled on liken-1 on 2026-08-17.

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

Delivering hotplug events changes unplug as well. Without them,
Weston never learns that a monitor left. With them, Weston destroys
the output when the connector disconnects and recreates it on replug.
The drill measured what that costs the client: nothing. The client's
Wayland connection never breaks, its surface re-homes when the output
returns, and only the taint's `tolerationSeconds` bounds how long the
screen may stay dark before the pod is evicted. An unplug can never
cost more than the one screen whose cable moved.

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

## What the drill showed

All four drills ran on liken-1 on 2026-08-17, against the release
2026.08.17-013, with the portable monitor on HDMI-A-2 and a movie
playing on HDMI-A-1 throughout.

1. **The event, byte for byte, passed.** A pod with a listener on
   netlink group 1 captured the unplug and the replug. Each one is a
   `change` on `/devices/pci0000:00/0000:00:02.0/drm/card1` with
   `SUBSYSTEM=drm`, `HOTPLUG=1`, and `CONNECTOR=400`, the KMS object
   id of HDMI-A-2. The kernel names the card, never the connector
   string.
2. **A monitor arrives, nothing else moves, passed.** The operator
   started with HDMI-A-2 dark, and its log showed the head found,
   disconnected, with its section written. A fresh claim and its pod
   parked `Pending`; the scheduler did not allocate the tainted dark
   device even though the pod tolerates `disconnected` for 30
   seconds, which matches what the Bluetooth operator's drills
   showed. The plug-in ran the whole chain inside one second: the
   datagram, the head connected with the BOE EDID, `Output 'HDMI-A-2'
   enabled`, the taint dropped, the claim allocated, and the pod
   `Running`. The operator was never restarted, and the movie's pod
   showed zero restarts.
3. **A monitor leaves, passed.** Weston disabled the output in the
   same second as the datagram. The orphaned surface never covered
   the other screen; the movie kept playing untouched.
   `DeviceTaintManagerEviction` evicted the holder on its 30 second
   toleration.
4. **A reseated cable, passed.** A five second unplug and replug put
   the taint on and took it off, and Weston disabled and re-enabled
   the output, twice within 244 ms on the replug as the link
   retrained. The pod survived with zero restarts and its picture
   came back on its own, because the client's Wayland connection
   never broke and its surface re-homed when the output returned. A
   bump within the toleration costs nothing.
