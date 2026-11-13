# 97. A Kubernetes-native boot console

Milestone 97. Proposed 2026-11-13, as a stub. Nothing is built, and
the design below is a starting point, not a decision. This milestone
includes milestone 75, a boot splash, which was proposed on 2026-10-08
as an investigation and was not started. Both milestones decide what
the screen of a machine shows during a boot, so they are one design.

## The problem

A machine with a screen shows the kernel's console from power-on
until the display-operator's compositor takes the card. The console
shows every printk record and every line init writes through
`/dev/kmsg` (`init/console.go`), and k3s writes its own output to the
same console. That text has two kinds of reader, and it serves
neither of them well.

The first reader watches the boot to learn how it goes. The lines
scroll faster than a person can read them, and they are the text of a
Linux boot. They do not show what a `liken` machine is for, which is
to become a node of a cluster and run its pods. When a boot is slow
or stops, the screen does not show whether the machine has an
address, whether it joined the cluster, whether its node is `Ready`,
or which pods do not run yet and why. The cluster holds those answers
as objects: the `Machine` has a phase and conditions, the `Node` has
conditions, each pod on the node has a status, and the components
post Events. A person reads them with `kubectl` from another
computer, and the screen that is connected to the machine shows none
of them.

The second reader is in a room with a television or a monitor, and
does not want to read a boot at all. That person sees the text during
each boot and each rollout reboot, where an image or an animated
`liken` mark would be better.

## The idea

One program draws the screen from the start of the boot until the
compositor takes the card, and a person chooses what it draws:

- `console`: the kernel's console, as it is today.
- `status`: the boot in the objects of the cluster, the same record
  that `kubectl` shows. The person at the screen reads the machine's
  `Machine`, its `Node`, and the pods bound to it, with what each one
  waits for.
- `logo`: the `liken` mark, or an image that the person supplies.

The three modes share the drawing, the handoff to the compositor, the
field that a person sets, and the rule for a boot that fails. In each
mode, the scrolling log is one key away.

[bootvine](https://codeberg.org/wvhulle/bootvine) is a good example of
the form of the `status` mode. On a machine that boots with systemd,
it replaces the scrolling log with a tree of the jobs that have not
finished, keeps a failed unit on the screen with its last log lines,
and ends with the units that took the longest. `liken` has no systemd
and does not adopt bootvine. The units of a `liken` boot are
Kubernetes objects, so `liken` builds its own console from them.

## What to keep

The console log is the record of a boot that went wrong, and a person
at the screen reads it when nothing else answers. So no mode hides
that record where it matters:

- The serial console keeps every line. The lab sets `console=ttyS0`
  (`liken/dev-cluster/Makefile`), and a change to what the screen
  shows changes nothing there.
- A panic, a failed `switch_root`, and init's refusals still reach a
  screen. A logo or a status that stays up over a machine that
  stopped is worse than the text it replaced.
- The logs still reach the cluster through the `liken-logs` relay,
  which reads the ring buffer and not the screen.

## A starting point for the `status` mode

A boot has two parts, and the `status` mode shows them as one record.

Before the API server answers, only init has facts: the manifest it
chose, the disks, the addresses, the clock, and the start of k3s. Init
already records these facts for the `Machine`'s status, and
`machine/status.go` describes several of them as the same facts that
init prints to the console. So this part of the screen comes from
that record, in the status's own field names, and not from a second
set of words.

After the API server answers, the screen reads the cluster:

- The `Machine`: its phase, and each condition that is not `True`
  with its reason and message.
- The `Node`: whether it is registered, its conditions, and its
  taints.
- The pods bound to this node that do not run yet. Each one shows
  what it waits for: an image pull, a volume, a `ResourceClaim`, an
  init container, or a restart backoff.
- The Events about those objects, in place of log lines.
- The reboot turn, when the boot is one machine's turn of a rollout:
  the release it boots, and whether the boot is the trial of a staged
  release.

When every condition is `True` and the pods run, the screen shows how
long each part took. A reboot and a power-off show the same objects
as the node drains and its pods stop.

The record is also in the cluster. Init or the machine operator
writes the time of each part into the `Machine`'s `status.boot`, so a
slow boot shows in `kubectl` and not only on the screen.

## Questions to answer

1. **What the kernel offers.** `liken` runs Ubuntu's mainline kernel
   build (`kernel/fetch.sh`), so the kernel's config is Canonical's and
   not this project's. Check which of these that build enables:
   `CONFIG_FRAMEBUFFER_CONSOLE_DEFERRED_TAKEOVER`, which leaves the
   firmware's logo on the screen until the first text reaches the
   console; the `simpledrm` driver on the firmware's framebuffer; and
   `CONFIG_LOGO`. Measure what each one shows on liken-1 and stick-1
   with `quiet` on the command line.
2. **Who draws the screen.** The options to compare:
   - The firmware's own logo, kept by deferred takeover and `quiet`.
     It costs nothing, the picture is the board vendor's and not
     `liken`'s, and it cannot show the `status` mode.
   - Init draws to the framebuffer or to a DRM dumb buffer, from the
     moment `/dev` exists until the compositor starts. Init is PID 1,
     so the drawing must not be able to block it, for the reason
     `init/console.go` gives for its log drainers.
   - A small program that init starts, such as Plymouth's DRM
     renderer without systemd, or a purpose-built Go binary. Find what
     it adds to the image and whether it can run with no seat manager.
   - A pod that claims the screen through the display-operator. This
     is the most native choice for the `status` mode, but no pod runs
     until the kubelet does, so init or its program draws the first
     part and gives the screen to the pod.
3. **Text or pixels.** A logo needs the framebuffer or DRM. The
   `status` mode can draw text on a virtual terminal, as bootvine
   does, or draw pixels with the same code as the logo. Decide
   whether the two modes share one drawing path.
4. **How the `status` mode reads the cluster.** Decide which
   credential it uses, and whether it watches the API server or reads
   the kubelet on the same machine. A follower that cannot reach a
   leader has no API server to read, and the screen must show that
   fact and not an empty tree.
5. **How the log stays readable.** In the `status` and `logo` modes
   the kernel still writes to the console. Decide whether the
   kernel's console log level goes down and the drawing takes a
   virtual terminal of its own, and which key shows the log again.
6. **The handoff to the compositor.** Whatever draws the screen holds
   the card's DRM master or the framebuffer until weston opens the
   card. Find out how to give the card up without a flash of the
   console between the two, and whether the display-operator's card
   gate (`display-operator/cardgate.go`) needs to know.
7. **How a person chooses.** A field on the `Cluster` or the
   `Machine`, such as `spec.boot.screen` with `console`, `status`,
   `logo`, or an image a person supplies, versus a kernel
   command-line flag in the boot entry. The choice has to take effect
   at the next boot. It must also survive a rollback to the other
   slot, so find out where the boot entries read it from
   (`init/bootentries.go`). Decide which mode a machine shows when
   the person chose nothing.
8. **What the screen shows when a boot fails.** Decide when the
   `status` and `logo` modes give way to the console: on a kernel
   panic, on a Go runtime panic in PID 1, on init's own errors and
   the refusals of `failBoot`, and after a time limit with no
   compositor.
9. **What a machine without a screen shows.** A serial line keeps
   every line of the log. Decide whether the `status` mode also
   writes a plain form to a serial line, or draws only on a screen.
10. **How much of the cluster one screen shows.** A screen in a room
    can show the names of pods and the text of Events to anyone who
    walks past. Decide which namespaces the `status` mode shows, and
    whether a person sets that.

## What the first step delivers

The first step is the investigation that milestone 75 proposed. It
delivers a short report in this document: what each kernel option
shows on the testbed's two machines, a recommendation among the
drawing options, and the field a person sets. The build of the three
modes becomes this milestone, or more than one new milestone if the
recommendation is large.
