# 75. A boot splash

Milestone 75. Proposed 2026-10-08, as an investigation. A machine with
a screen shows the kernel's console from power-on until the
display-operator's compositor takes the card: every printk record and
every line init writes through `/dev/kmsg` (`init/console.go`). On a
television or a monitor in a room, that text is what a person sees
during each boot and each rollout reboot. This milestone finds out
whether a machine can show an image or an animated `liken` mark
instead, as a choice the person makes, and what that choice costs.

## What to keep

The console log is the record of a boot that went wrong, and a person
at the screen reads it when nothing else answers. So a splash must not
hide that record where it matters:

- The serial console keeps every line. The lab sets `console=ttyS0`
  (`Makefile`), and a splash on the screen changes nothing there.
- A panic, a failed `switch_root`, and init's refusals still reach a
  screen. A splash that stays up over a machine that stopped is worse
  than the text it replaced.
- The logs still reach the cluster through the `liken-logs` relay,
  which reads the ring buffer and not the screen.

## Questions to answer

1. **What the kernel offers.** `liken` runs Ubuntu's mainline kernel
   build (`kernel/fetch.sh`), so the kernel's config is Canonical's and
   not this project's. Check which of these that build enables:
   `CONFIG_FRAMEBUFFER_CONSOLE_DEFERRED_TAKEOVER`, which leaves the
   firmware's logo on the screen until the first text reaches the
   console; the `simpledrm` driver on the firmware's framebuffer; and
   `CONFIG_LOGO`. Measure what each one shows on liken-1 and stick-1
   with `quiet` on the command line.
2. **Who draws the splash.** The options to compare:
   - The firmware's own logo, kept by deferred takeover and `quiet`.
     It costs nothing, and the picture is the board vendor's, not
     `liken`'s.
   - Init draws an image or a short animation to the framebuffer or
     to a DRM dumb buffer, from the moment `/dev` exists until the
     compositor starts. Init is PID 1, so the drawing must not be able
     to block it, for the reason `init/console.go` gives for its log
     drainers.
   - A small program that init starts, such as Plymouth's DRM
     renderer without systemd, or a purpose-built Go binary. Find what
     it adds to the image and whether it can run with no seat manager.
3. **The handoff to the compositor.** Whatever draws the splash holds
   the card's DRM master or the framebuffer until weston opens the
   card. Find out how to give the card up without a flash of the
   console between the two, and whether the display-operator's card
   gate (`display-operator/cardgate.go`) needs to know.
4. **How a person chooses.** A field on the `Cluster` or the
   `Machine`, such as `spec.boot.splash` with `console`, `logo`, or an
   image a person supplies, versus a kernel command-line flag in the
   boot entry. The choice has to take effect at the next boot. It must
   also survive a rollback to the other slot, so find out where the
   boot entries read it from (`init/bootentries.go`).
5. **What the screen shows when a boot fails.** Decide when the
   splash gives way to the console: on a kernel panic, on init's own
   errors, and after a time limit with no compositor.

## What the investigation delivers

A short report in this document: what each kernel option shows on the
testbed's two machines, a recommendation among the drawing options,
and the field a person sets. The build becomes this milestone, or a
new one if the recommendation is large.
