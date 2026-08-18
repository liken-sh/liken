# The display operator

Milestone 57. Completed. Each monitor output publishes as its own DRA
device, so a pod claims one screen by its connector name or by what
the monitor is, and receives the Wayland socket and the app-id that
put its window on that screen. It is the first instance of
milestone 56's pattern: the operator claims the GPU's display device
through an ordinary `liken.sh` claim, runs the compositor, and
publishes what the compositor holds under `display.liken.sh`.

## What already runs

The lab runs the compositor half of this on `liken-1`, an Intel N95
with two monitors on it, and the pod is fully unprivileged. It declares
no `hostNetwork`, and it adds no capability. Everything it does to the
hardware, it does through one DRA claim.

* The pod claims the graphics device's display companion, the
  `-display` device that milestone 49 publishes for a GPU's card node.
  The claim is exclusive, which is what milestone 49 decided for it: a
  card node holds display authority, and two display programs on one
  card have no arbitration contract.
* It runs Weston 14 with the kiosk shell. The shell makes every client
  fullscreen on one output, which is what a screen in a house is for:
  one program, no decorations, no desktop.
* Clients are separate pods. They reach the compositor over a Wayland
  socket on a shared PVC, and they hold no device claim of their own.
* A client lands on an output by its app-id. `weston.ini` names the
  app-ids for each output with an `app-ids=` line. A client sets its
  own: chromium takes `--class`, and mpv takes `--wayland-app-id`.

The lab ran two clients on the two monitors at once, each on the output
its app-id named, with a transcode running on the same GPU through the
render node's separate shareable device.

## What nothing arbitrates

The app-id routing works, and it is a naming convention with nothing
behind it. Two failures follow from that, and the lab met both.

* **Two clients may present the same app-id.** Nothing refuses the
  second one. The lab started a second chromium with the same
  `--class`, and its surface covered the first one on the same output.
  The first client stays running, stays connected, and stays invisible.
  The cluster shows two healthy pods.
* **An app-id that matches no output still gets a screen.** The kiosk
  shell sends a surface whose app-id matches nothing to the first
  output. So a typo in a client's flag does not fail the client. It
  puts the client on whichever monitor the compositor enumerated first,
  on top of whatever was already there.

Both have the same cause: the output is not a resource that anything
allocates. It is a string in a config file that a client repeats. A
person who wants a second screen also has to edit `weston.ini`,
restart the compositor, and remember which string went where, and none
of that is in the cluster.

One more gap remains. A client pod needs the machine that has
the monitors, and today nothing states that. The lab's clients land on
the right machine only because the socket PVC's volume affinity places
them on the node where the volume happened to provision. The pod's own
spec says nothing about a screen, so the placement is an accident of
storage, and it holds only while the socket stays on that one kind of
volume.

## One device for each output

The operator publishes one device for each output the compositor
drives, in its own ResourceSlices, under `display.liken.sh`.

The device name is the connector name that the kernel assigns:
`hdmi-a-1`, lowercased for the DNS label a device name must be. A
connector name is a property of the graphics device's own outputs, so
it is stable across reboots on one machine. It is not stable across
machines, and it says nothing about which monitor is plugged in, which
is why the EDID facts publish beside it.

The attributes come from the monitor's EDID, which the compositor
already reads:

* the manufacturer and the model name,
* the serial number,
* the current mode's width and height in pixels,
* the physical size in millimeters.

Two kinds of claim follow from that, and both are useful. A claim can
name one screen, by model or by serial, which is the claim a dashboard
on the kitchen monitor takes. Or a claim can select on the attributes
and take any output that fits, which is the claim a video player takes:
any output at 1920x1080 or better, whichever one is free.

A published output is exclusive. That is the whole point of publishing
it: one fullscreen client on one screen stops being a convention and
becomes a property the scheduler holds. A second pod that asks for the
same screen parks until the first one ends, and a second pod that asks
for any 1080p screen lands on the other monitor. Nothing covers a
running client.

The claim places the client as well. Only the machine with the
monitors publishes an output, so a pod that claims a screen runs on
that machine. The placement stops depending on where the socket volume
happened to provision, and it stops depending on the app-id string a
person remembered to type.

## The delivery is not a device node

This is where the display operator differs in structure from the
Bluetooth operator.

A consumer of a Bluetooth controller receives a device node, and a
device node is what liken's own driver delivers. A Wayland client
receives no device node at all. What it needs is two other things: the
compositor's socket, and the app-id that the compositor routes to the
claimed output.

CDI delivers both. A CDI device may inject environment variables and
mounts as well as device nodes, and the operator's CDI spec uses them:

* `WAYLAND_DISPLAY`, and the mount of the socket directory the
  compositor listens on.
* the app-id for the allocated output, in the variable the client reads
  to set it.

liken's own driver injects device nodes only, and that is a decision,
not a limit. The OS driver delivers hardware, and hardware is nodes. An
operator stacked on top of it is a different layer with a different
job, so it may use the rest of what CDI defines. The pattern in
milestone 56 states that each instance says what its delivery is, and
this is the instance where the answer is not a node.

One consequence is worth stating plainly. A client still has to pass
the app-id to its own toolkit, because no Wayland client reads an
environment variable that chromium and mpv do not define. The pod spec
has the flag, and the variable is what the flag reads from:
`--class=$(DISPLAY_APP_ID)`. The operator's job ends at making the
value correct and unique.

## A monitor that leaves

A monitor that a person unplugs is milestone 56's loss case, with
nothing new in it. The device stays published, the operator applies a
`NoExecute` taint to it, the taint-eviction controller ends the client
pod, and `tolerationSeconds` on the claim sets how long a monitor may
be dark before that happens. A monitor coming back clears the
taint, and the scheduler starts the client again.

The kernel's own hotplug is what the operator listens to. A connector
change is a drm uevent, so the operator re-reads the connector's
sysfs state on the event rather than on a timer, and it settles a
burst before it writes a slice, for the reason milestone 56 gives.
The operator does not consult Weston, because sysfs reports the same
fact without coupling the inventory to the compositor's IPC.

The compositor's routing is narrower than the inventory. The operator
writes `weston.ini` once, at its own start, so a connector that was
dark then has no routing section while the operator runs. Such a
connector publishes with the `NoSchedule` taint even after a monitor
arrives on it, because a client whose app-id matches no section lands
on the first output, on top of that output's rightful client. The
claim parks instead, and an operator restart is what adopts the new
connector.

## What a drill must show

The drill runs on `liken-1` with both monitors connected.

* **The Kustomization deploys it and deleting it removes it.** Before
  the apply, the machine publishes what it publishes today and no
  `display.liken.sh` slice exists. Apply the published base, and the
  operator's pod starts on the machine with the display device, with no
  node selector written by hand. Delete it, and the published devices
  return to what they were.
* **Each output publishes once.** Two devices appear, named for the two
  connectors, each with its own monitor's model, serial, mode, and
  physical size.
* **A claim by name.** A pod that names one connector starts on that
  monitor. A pod that selects on the model of the other monitor starts
  on the other one.
* **A claim by attribute.** Two pods that both ask for any 1080p output
  land on the two different monitors, and a third parks as
  Unschedulable until one of them ends.
* **Exclusivity holds where the convention failed.** The two-chromium
  case from "What nothing arbitrates" must be unreachable: the second
  pod cannot allocate an output that the first pod holds, so nothing
  covers the first client's surface.
* **A wrong name fails visibly.** A claim naming a connector that does
  not exist parks as Unschedulable. It must not land on the first
  output.
* **Unplug and replug.** With a `tolerationSeconds` of thirty, a five
  second unplug must not end the client pod, and an unplug past thirty
  seconds must end it. Replugging clears the taint and the client
  starts again, with no human step.
* **An operator restart while a claim is live.** Deleting the operator
  pod restarts the compositor, so the client pod loses its socket. The
  drill must show what the client does and what the operator does when
  it comes back, because this is the one place where the display
  operator's restart is worse than the Bluetooth operator's: prepared
  CDI files survive a restart, and a Wayland connection does not.

## Open questions

These were the questions this milestone could not answer. Each one
below records what happened to it.

* **Static app-ids or minted ones.** Still open, and it belongs to the
  operator now. The shipped release uses fixed strings, written into
  `weston.ini` once, which is the simpler half of the choice this
  milestone framed. The case for minting them is in
  [Routing is narrower than inventory](https://github.com/liken-sh/display-operator/blob/main/plans/open-problems/routing-is-narrower-than-inventory.md).
* **How the compositor gets the mapping.** Still open, and it is the
  same question as the one above, for the reason this milestone gave:
  minting an app-id per allocation changes the routing table while the
  compositor runs. The one document above covers both halves.
* **Where the HDMI audio belongs.** Answered, and drilled. Audio stays
  its own operator. A client gets a screen and that screen's speakers
  from one claim holding a request against each driver, joined by a
  `matchAttribute` constraint on `monitor.liken.sh/id`, which both
  drivers derive byte for byte from the same monitor: the display
  operator from its EDID, the audio operator from the PCM's ELD. On
  liken-1 that matched the LG as `gsm-7716-lg-hdr-wqhd` from both
  sides. The cost this milestone named arrived with the answer: the
  two operators share an attribute domain, and five parity test
  vectors in each repository keep the two derivations identical. See
  [milestone 59](59-the-audio-operator.md).
