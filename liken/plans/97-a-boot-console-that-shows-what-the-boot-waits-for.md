# 97. A boot console that shows what the boot waits for

Milestone 97. Proposed 2026-11-13, as a stub. Nothing is built, and
the design below is a starting point, not a decision.

## The problem

A person who watches a `liken` machine boot reads the kernel's
console. It shows every printk record and every line init writes
through `/dev/kmsg` (`init/console.go`), and k3s writes its own output
to the same console. The lines scroll faster than a person can read
them. When a boot is slow or stops, the screen does not show which
step the boot is in, how long that step has run, or what it waits
for. The person has to know the boot well enough to find that in the
text.

## The idea

[bootvine](https://codeberg.org/wvhulle/bootvine) solves this problem
for a machine that boots with systemd. It replaces the scrolling log
with a tree of the systemd jobs that have not finished, and each unit
is drawn under the unit that waits for it. A unit that fails stays on
the screen with its result and its last journal lines. When the tree
is empty, bootvine shows a timeline of the units that took the longest.
It also shows a shutdown in the same form.

`liken` cannot run bootvine. bootvine reads its tree from systemd's
job queue through libdbus and reads the failed unit's lines from the
journal through libsystemd, and it needs systemd as PID 1. On a
`liken` machine, init is PID 1, the machine-plane components are
goroutines inside it, and k3s is the only child process
(`init/components.go`). So this milestone takes the design of the
screen and not the program: `liken` draws its own boot in that form.
bootvine's license is GPL-3.0-or-later and `liken`'s is MIT, so the
build reads bootvine's documents for the design and copies none of
its code.

## A starting point for the design

Init already holds what the screen needs, because the order of a boot
is the order of init's program. Init can record each step as it
starts and as it ends: the step's name, its start time, and what it
waits for, such as a disk, an address, a time source, or a release
download. A drawing loop reads those records and paints the console.
The drawing must not be able to block PID 1, for the reason
`init/console.go` gives for its log drainers.

The screen shows these things:

- The steps that run now, each with the time it has run and what it
  waits for.
- A step that failed, with its error and the rationale that
  `failBoot` prints today, for as long as the machine has power.
- When the boot is done, the steps that took the longest.
- A reboot and a power-off in the same form, as k3s stops, the
  machine plane stops, and the filesystems unmount.

## Questions to answer

1. **Where init's part of the boot ends.** A person calls a machine
   booted when its node is `Ready` and its pods run, and that is long
   after init starts k3s. Find out what the screen shows for that
   part of the boot, and where init reads it from: the k3s log that
   init already tails, the kubelet, or the machine operator.
2. **How the log stays readable.** The kernel still writes to the
   console that the screen draws on. bootvine lowers the console log
   level and draws on a virtual terminal of its own. Decide whether
   `liken` does the same, and which key shows the log again.
3. **What a machine without a screen shows.** The lab reads the
   serial console (`console=ttyS0`), and a serial line has no size
   and often no terminal. The serial console must keep every line, as
   [milestone 75](75-a-boot-splash.md) requires. Decide whether the
   tree draws only on a virtual terminal, or also in a plain form on
   a serial line.
4. **What a panic shows.** A kernel panic and a Go runtime panic in
   PID 1 must still reach the screen, with nothing drawn over them.
5. **How this fits with the boot splash.** Milestone 75 asks whether
   a machine can show an image during the boot, and this milestone
   shows text. They share the handoff to the display-operator's
   compositor, the choice a person makes, and the rule for a boot
   that fails. Decide whether they are one field with several values,
   such as `console`, `status`, and `logo`, and which one a machine
   shows when the person chose nothing.
6. **Whether the cluster gets the same record.** The times of each
   step are useful after the boot too. Decide whether init publishes
   them in the `Machine`'s `status.boot`, so that a slow boot shows
   in `kubectl` and not only on the screen.

## What is not in scope

A passphrase prompt. bootvine answers a LUKS unlock on its screen,
and `liken` asks for no passphrase during a boot.
