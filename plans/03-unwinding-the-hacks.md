# Unwinding the known hacks

Milestone 3 — Done

Unwind the known hacks before building anything on top of them.
These are fixes from the boot-to-k3s work. Each fix depends on
k3s internals that k3s never promised to keep. Each fix works
today, and the version pin and `make run-once` guard it. But
every milestone below builds on the boot path, so the team
settles this coupling first.
1. [x] Init's PATH variable hardcoded k3s's internal layout. This
   turned out to be redundant, so the team removed it. The
   console shows k3s prepending its own unpacked userland to the
   PATH it builds for child processes. The cluster settles
   without the extra entries.
2. [x] The `/sbin/iptables` dangling symlinks are gone. The
   netfilter userspace is now its own vendored domain
   (`xtables/`). It is fetched from k3s-root, the same project
   that builds k3s's bundled copy, and pinned to the same
   version the vendored k3s uses. From the image build onward,
   `/sbin/iptables` is a real static binary. The machine also
   reports its xtables version in the Machine's status.version.
   It observes this version with `iptables -V`, the same way it
   observes every other fact.
3. [x] Run switch_root onto a plain tmpfs early in boot, the way
   k3OS did. This makes the root filesystem an ordinary
   measurable mount, instead of the kernel's special initramfs
   rootfs. This change let the team drop
   `local-storage-capacity-isolation=false` entirely, and it
   stopped kubelet's recurring filesystem-stat errors. kubelet
   now measures / the same way it would measure it on any other
   machine.
4. [x] The CA bundle came from whichever machine ran the build;
   build.sh's own comment said as much. The build now vendors
   the CA bundle like everything else: a `trust/` domain pins a
   dated snapshot of the Mozilla bundle. Because of this, what
   the machine trusts changes by a reviewable version bump, not
   by an accident of which host ran the build.
