# Unwinding the known hacks

Milestone 3 — Done

Unwind the known hacks before building on top of them. These are
fixes from the boot-to-k3s work that depend on k3s internals it
never promised us. Each works today and is guarded by the
version pin and `make run-once`, but every milestone below
builds on the boot path, so the coupling gets settled first.
1. [x] Init's PATH hardcoded k3s's internal layout, and it turned
   out to be redundant, so it is removed. The console shows k3s
   prepending its own unpacked userland to the PATHs it builds
   for children, and the cluster settles without the extra
   entries.
2. [x] The `/sbin/iptables` dangling symlinks are gone: the
   netfilter userspace is now its own vendored domain
   (`xtables/`), fetched from k3s-root, the same project that
   builds k3s's bundled copy, pinned to the same version the
   vendored k3s uses. `/sbin/iptables` is now a real static
   binary from the image build onward. The machine also reports
   its xtables version in the Machine's status.version, observed
   via `iptables -V` like every other fact.
3. [x] switch_root onto a plain tmpfs early in boot, the way k3OS
   did, so the root filesystem is an ordinary measurable mount
   instead of the kernel's magic initramfs rootfs. This let us
   drop `local-storage-capacity-isolation=false` entirely and
   silenced kubelet's recurring filesystem-stat errors; kubelet
   now measures / the way it would on any other machine.
4. [x] The CA bundle came from whichever machine ran the build
   (build.sh's own comment said as much). It is now vendored
   like everything else: a `trust/` domain pins a dated snapshot
   of the Mozilla bundle, so what the machine trusts changes by
   a reviewable version bump instead of by build-host accident.
