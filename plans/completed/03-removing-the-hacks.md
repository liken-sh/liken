# Remove the known hacks

Milestone 03 — Completed. The boot path drops the fixes that depend on
k3s internals.

These fixes come from the boot-to-k3s work. Each fix depends on k3s
internals that k3s does not promise to keep. Each fix works today, and
the version pin and `make run-once` protect them. Every milestone below
builds on the boot path, so the project removes this coupling first.
1. [x] The PATH variable in init hardcoded the internal k3s layout.
   These entries are redundant, so they are gone. The console shows k3s
   put its own unpacked userland at the front of the PATH that it builds
   for child processes. The cluster settles without the extra entries.
2. [x] The `/sbin/iptables` dangling symlinks are gone. The netfilter
   userspace is now its own vendored domain (`xtables/`). It comes from
   k3s-root, the same project that builds the bundled copy in k3s, and
   it is pinned to the same version that the vendored k3s uses. From the
   image build onward, `/sbin/iptables` is a real static binary. The
   machine also reports its xtables version in the Machine
   status.version. It observes this version with `iptables -V`, the same
   way it observes every other fact.
3. [x] Run switch_root onto a plain tmpfs early in the boot, the way
   k3OS did. This makes the root filesystem an ordinary measurable
   mount, in place of the special initramfs rootfs of the kernel. This
   change removed the need for `local-storage-capacity-isolation=false`,
   and it stopped the recurring filesystem-stat errors from kubelet.
   kubelet now measures / the same way it measures / on any other
   machine.
4. [x] The CA bundle came from the machine that ran the build, as the
   comment in build.sh said. The build now vendors the CA bundle like
   everything else: a `trust/` domain pins a dated snapshot of the
   Mozilla bundle. Thus what the machine trusts changes by a version
   bump that a person can review, and not by the choice of build host.
