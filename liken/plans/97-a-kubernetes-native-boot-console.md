# 97. A Kubernetes-native boot console

Milestone 97. Proposed 2026-11-13, as a stub. Nothing is built, and
the design below is a starting point, not a decision.

## The problem

A person who watches a `liken` machine boot reads the kernel's
console. It shows every printk record and every line init writes
through `/dev/kmsg` (`init/console.go`), and k3s writes its own output
to the same console. The lines scroll faster than a person can read
them, and they are the text of a Linux boot. They do not show what a
`liken` machine is for, which is to become a node of a cluster and run
its pods. When a boot is slow or stops, the screen does not show
whether the machine has an address, whether it joined the cluster,
whether its node is `Ready`, or which pods do not run yet and why.

The cluster holds those answers as objects. The `Machine` has a phase
and conditions, the `Node` has conditions, each pod on the node has a
status, and the components post Events. A person reads them with
`kubectl` from another computer. The screen that is connected to the
machine shows none of them.

## The idea

The boot console of a `liken` machine shows the boot in the objects of
the cluster, and it shows the same record that `kubectl` shows. The
person at the screen reads the machine's `Machine`, its `Node`, and
the pods bound to it, with what each one waits for, and the scrolling
log is one key away.

[bootvine](https://codeberg.org/wvhulle/bootvine) is a good example of
the form. On a machine that boots with systemd, it replaces the
scrolling log with a tree of the jobs that have not finished, keeps a
failed unit on the screen with its last log lines, and ends with the
units that took the longest. `liken` has no systemd and does not
adopt bootvine. The units of a `liken` boot are Kubernetes objects,
so `liken` builds its own console from them.

## A starting point for the design

A boot has two parts, and the console shows them as one record.

Before the API server answers, only init has facts: the manifest it
chose, the disks, the addresses, the clock, and the start of k3s. Init
already records these facts for the `Machine`'s status, and
`machine/status.go` describes several of them as the same facts that
init prints to the console. So this part of the console comes from
that record, in the status's own field names, and not from a second
set of words.

After the API server answers, the console reads the cluster:

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

When every condition is `True` and the pods run, the console shows
how long each part took. A reboot and a power-off show the same
objects as the node drains and its pods stop.

The record is also in the cluster. Init or the machine operator
writes the time of each part into the `Machine`'s `status.boot`, so a
slow boot shows in `kubectl` and not only on the screen.

## Questions to answer

1. **Who draws the console.** Init can draw from the first second,
   and it must not block PID 1, for the reason `init/console.go` gives
   for its log drainers. The machine operator holds a credential and
   the watches already. A pod that claims the screen through the
   display-operator is the most native choice, but no pod runs until
   the kubelet does. Decide whether one program draws the whole boot,
   or init draws the first part and gives the screen to a pod.
2. **How the console reads the cluster.** Decide which credential it
   uses, and whether it watches the API server or reads the kubelet on
   the same machine. A follower that cannot reach a leader has no API
   server to read, and the console must show that fact and not an
   empty screen.
3. **How the log stays readable.** The kernel still writes to the
   console that the screen draws on. Decide whether the kernel's
   console log level goes down and the drawing takes a virtual
   terminal of its own, and which key shows the log again.
4. **What a machine without a screen shows.** The lab reads the
   serial console (`console=ttyS0`), and the serial console must keep
   every line, as [milestone 75](75-a-boot-splash.md) requires.
   Decide whether the console draws only on a virtual terminal, or
   also in a plain form on a serial line.
5. **What a failed boot shows.** A kernel panic, a Go runtime panic
   in PID 1, and the refusals of `failBoot` must reach the screen
   with nothing drawn over them.
6. **How much of the cluster one screen shows.** A screen in a room
   can show the names of pods and the text of Events to anyone who
   walks past. Decide which namespaces the console shows, and whether
   a person sets that.
7. **How this fits with the boot splash.** Milestone 75 asks whether
   a machine can show an image during the boot. The two milestones
   share the handoff to the display-operator's compositor, the choice
   a person makes, and the rule for a boot that fails. Decide whether
   they are one field with several values, such as `console`,
   `status`, and `logo`, and which one a machine shows when the
   person chose nothing.
