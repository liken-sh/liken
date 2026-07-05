# The rough path

1. [x] Boot to a hello world from an init I built: `make run` boots QEMU
       and PID 1 speaks on the serial console. (There is no shell and no
       prompt — the console is output-only, by design.)
   1. [x] `kernel/`: vendor a pre-built vanilla kernel from Ubuntu's
          mainline builds — fetch a pinned version, verify checksums,
          extract the image and modules, run `depmod` at build time.
   2. [x] `init/`: a minimal Go init that mounts `/proc`, `/sys`, and
          `/dev`, prints a report of the world it woke up in, and reaps.
   3. [x] `image/`: assemble the initramfs — a cpio archive; the whole OS
          is `vmlinuz` plus `liken.cpio`.
   4. [x] `make run`: boot it headless in QEMU; a smoke test can watch the
          serial output for a marker, which is CI in embryo. Use explicit
          flags (`-display none -serial stdio -monitor none -no-reboot`)
          rather than the `-nographic` bundle, so each flag can explain
          itself.
2. [x] Init starts k3s and nothing else — and discover every host dependency
       (cgroups, kernel modules, time, entropy) the hard way.
   1. [x] Boot to network from a Machine manifest (`kind: Machine`,
          `apiVersion: liken.sh/v1alpha1`, file-delivered at boot): raise
          the interface, speak DHCP (the whole DORA exchange prints to
          the console), apply the lease via netlink, and prove it with a
          DNS lookup against an outside nameserver. (Entropy was the
          predicted hard-way discovery: no RDRAND → kernel RNG never
          initializes → getrandom() blocks forever in the DHCP client.)
   2. [x] Boot to a Ready node: init builds the world k3s assumes
          (cgroup2, identity files, mount propagation, module preload,
          the shell-less iptables story), supervises it with backoff,
          and narrates node and pod state to the console. `make run`
          ends at a settled single-node cluster; `make run-once`
          (`liken.oneshot`) makes any k3s death power the machine off
          so a harness can measure the boot.
   3. [x] Machine identity is an input, not an output: `make` mints a
          CA bundle (gitignored, identity/) and pre-seeds k3s's TLS
          directory in the image, so an operator's kubeconfig is
          computed offline — never copied off the machine. `make
          kubeconfig` plus a loopback-only QEMU port-forward gets
          `kubectl get nodes` from the host; no `--tls-san` needed,
          since k3s's serving cert covers 127.0.0.1 by default. (The
          hard-way discovery: kube-apiserver reads the ServiceAccount
          key with a parser that takes SEC1 "EC PRIVATE KEY" but not
          PKCS#8.)
   4. [x] The Kubernetes API is the machine API: the Machine manifest is
          now a real CRD (`kubectl get machines` works), reconciled by a
          liken operator that ships inside the initramfs as a
          hand-assembled OCI tarball (operator/image.sh) and deploys
          through k3s's auto-manifests directory — zero registry pulls,
          zero kubectl steps. Init never talks to k3s: it applies
          spec.sysctls at boot and writes facts to `/run/liken/`; the
          operator (plain net/http against the API server — no
          client-go, the watch loop is the lesson) seeds the Machine
          from the manifest, publishes facts + observed sysctls into
          status, and reconciles sysctl edits live. The shared Go types
          live in the machine/ domain, used by both programs. (A
          leftover mystery got mostly solved on the way: "The manifest
          file is empty, ignoring." fires once per embedded
          control-plane component as it parses its options — unrelated
          to k3s's manifests directory.)
3. [x] Unwind the known hacks before building on top of them. These are
       fixes from the boot-to-k3s work that encode knowledge k3s never
       promised us; each works today and is guarded by the version pin
       + `make run-once`, but every milestone below stacks more weight
       on the boot path, so the coupling gets settled first.
   1. [x] Init's PATH hardcoded k3s's internal layout — it was indeed
          redundant. Removed; the console shows k3s prepending its own
          unpacked userland to the PATHs it builds for children, and
          the cluster settles without the tail.
   2. [x] The `/sbin/iptables` dangling symlinks are gone: the
          netfilter userspace is now its own vendored domain
          (`xtables/`), fetched from k3s-root — the same project that
          builds k3s's bundled copy, pinned to the same version the
          vendored k3s uses — so `/sbin/iptables` is a real static
          binary from the image build onward. The machine also reports
          its xtables version in the Machine's status.version, observed
          via `iptables -V` like every other fact.
   3. [x] switch_root onto a plain tmpfs early in boot, the way k3OS
          did, so the root filesystem is an ordinary measurable mount
          instead of the kernel's magic initramfs rootfs. This let us
          drop `local-storage-capacity-isolation=false` entirely and
          silenced kubelet's recurring filesystem-stat errors — kubelet
          now measures / like any other machine's.
   4. [x] The CA bundle came from whichever machine ran the build
          (build.sh's own comment confessed it). Now vendored like
          everything else: a `trust/` domain pinning a dated snapshot
          of the Mozilla bundle, so what the machine trusts is a
          reviewable version bump instead of a build-host accident.
4. [x] Storage — which starts with a disk existing at all. The whole
       machine is RAM today; the prize is k3s's state on persistent
       storage (container images stop re-importing every boot, cluster
       state survives a reboot). Storage is declared by *purpose*, not
       by mount path: `spec.storage` is a map keyed by singleton role
       (`clusterState` first), each entry naming a device and an
       optional fixed size. liken derives GPT partition tables from the
       roles grouped by device, formats blank disks at runtime, and
       names each partition `liken:<role>` — so recognition on every
       later boot is by partition name read from sysfs (no udev;
       `device:` is first-boot claiming input only, since kernel
       enumeration order is not a promise). Reconciling never destroys
       data — blank → claim, ours → mount, anything foreign or
       ambiguous → refuse — and never serves on a broken promise: a
       declared role that can't be reconciled stops the boot (the full
       story on the console, then power off, never k3s), because a
       machine promised persistent cluster state that boots ephemeral
       anyway is a data-loss machine; down is recoverable, wrong is
       not. Undeclared roles simply land where everything lands today
       — the root tmpfs — and `status.storage` enumerates where every
       role actually landed (`Partition` or `Memory`), while
       `status.hardware.blockDevices` reports the raw inventory.
   1. [x] A disk exists: `make run` attaches a gitignored qcow2, and
          init discovers block devices from `/sys/block` — the world
          report learns a new question.
   2. [x] Claiming: init writes the GPT itself (it's a small,
          checksummed binary format — a good lesson), makes the
          filesystem (the one open mechanism: the image has no libc,
          so mkfs must be static or Go), and mounts `clusterState`
          under k3s's world, all before k3s starts. Every reason a
          spec can be refused (foreign disks, cloned disks, disks too
          small, partial claims) is unit-tested in init/, against
          fake sysfs trees; a refusal halts the boot from one place
          in main.go.
   3. [x] Prove persistence: boot, power off, boot again — images
          import once, the cluster comes back. (Proven by milestone
          5's reboot cycles: the cluster survived staged-spec reboots
          and a hard power cut, on the same disks.)
   4. [x] The API: `spec.storage` and `status.storage` in the Machine
          CRD, the operator publishing the landing table and the
          hardware inventory.
5. [x] The spec becomes editable: a Machine edit in the cluster
       actually converges, by reboot. The roles speak for their owners
       now (`machineState` and `machineEphemeral` are the machine's,
       `clusterState` awaits `kind: Cluster`), and the new
       `machineState` role holds the machine's manifests: the operator
       detects drift between the cluster's spec and the boot's
       boot record, validates against the machine's reality
       (grow-only sizes, attached devices; CEL rules refuse shrinks at
       admission), stages the manifest durably, and per
       `spec.rebootPolicy` requests a reboot or reports one pending.
       Init prefers the staged manifest, promotes it on success, and
       falls back to the proven last-known-good on failure, so a bad
       edit degrades the machine instead of downing it. Partitions are
       grow-only: sized roles grow into free space, remainder roles
       follow a grown disk (relocating the backup GPT), and ext4 grows
       by ioctl, no resize2fs.
   1. [x] The `machine*` role vocabulary, and `machineState` first in
          canonical order so a boot can find it before reading any spec.
   2. [x] A GPT reader (both copies, CRC-checked, identities preserved
          through edits) and grow-only partition resize, with the
          filesystem grown online via EXT4_IOC_RESIZE_FS.
   3. [x] The manifest lifecycle on machineState: staged/proven/
          rejected, durable writes, the settle loop with last-known-good
          fallback, and the boot record in facts and status.
   4. [x] The operator's convergence loop: drift detection, staging
          validation, the SpecConverged condition vocabulary,
          `spec.rebootPolicy`, and CEL no-shrink rules in the CRD.
   5. [x] The reboot protocol: the operator's intent file, init's
          watcher, a graceful k3s stop, and `make run-lab` (a QEMU run
          that survives reboots) plus `grow-pods` for the disk-growth
          drill.
   6. [x] Prove the full cycle in the lab: edit the spec via kubectl,
          watch the machine stage, reboot, grow, and converge; drill
          the rejections (CEL refuses a shrink at admission, the
          operator refuses an invalid spec with StagingRejected, and a
          staged spec that fails at boot falls back to proven and
          holds at RejectedLastBoot without a reboot loop). The
          disk-growth drill grew podEphemeral's partition and
          filesystem from 1.5 to 5.5 GiB in place.
   7. [x] Editing back to a good state. The first CEL rules compared
          the spec against its previous value, which wedged: after
          declaring a size the machine couldn't satisfy, reverting the
          spec was also refused as a shrink, and the only exit was
          `kubectl replace --force` — untenable once Flux owns the
          spec. The rules now compare the spec against
          `status.boot.storage` (the sizes the machine actually booted
          with), so a failed aspiration can always be edited back to
          reality, and only a real on-disk shrink is refused. When the
          spec returns to what the machine runs, the operator also
          withdraws any manifest still staged (the next boot would
          have applied it) and clears the standing rejection.
6. [ ] Bake in Flux bootstrap, so the machine converges to its repo from
       first boot. This is where the Machine manifest's authority story
       resolves: today the file seeds the cluster and the cluster copy
       wins; with GitOps, git wins and both are downstream of it. Open
       questions to answer on the way: where the git credentials live
       (they're identity, like the CA bundle), and how a machine learns
       *which* repo — another field the image carries, or part of the
       Machine manifest itself.
7. [ ] Growing the cluster past one node, driven by a `kind: Cluster`
       resource: what a machine needs to form or join a cluster — the
       k3s join token (or a reference to it; a token in plain YAML is a
       secrets problem), the server URL, and which machines are servers
       vs. agents. The Cluster could carry a template for `kind:
       Machine` resources that a new, unknown node *claims* on first
       boot — which is also where the "which machine am I?" open problem
       gets a real answer. The lab has to grow too: two QEMU guests that
       can reach each other is its own networking lesson (user-mode
       networking isolates guests; joining them takes a socket network
       or a bridge).
8. [ ] Explore device management: how does a shell-less, udev-less OS
       expose `/dev` beyond the basics — USB devices arriving after
       boot, GPUs, serial adapters? devtmpfs gives us the nodes, but
       hotplug means fielding kernel uevents and loading modules,
       which is the job udev does elsewhere. Then the Kubernetes half:
       how workloads get to the hardware (device plugins, dynamic
       resource allocation) and whether devices belong in
       `status.hardware` alongside CPUs and memory.
9. [ ] Explore declarative upgrades: setting `spec.version` on a Machine
       should upgrade the machine — liken's version drives the k3s
       version, so one field moves the whole stack. For a two-file OS an
       upgrade is "replace vmlinuz and liken.cpio and reboot", which
       makes A/B slots and roll-back-on-failed-boot the natural shape —
       but it also means liken finally needs a bootloader story, since
       QEMU's `-kernel` has been playing that role.

Deferred until the fundamentals above are proven — the
public-consumption tier:

* The public bootstrapping story: today the identity bundle is minted by
  make in a private checkout, but a released OS needs a way for anyone
  to mint theirs — an installer step, a tiny CLI, or a documented
  openssl recipe.
* The mastery tier: UKIs, dm-verity, secure boot, TPM-sealed secrets.

# Open problems

Questions we know we owe answers, without pretending to have them yet:

* **Which machine am I?** One image may boot many machines, so something
  has to definitively identify a machine and select its Machine manifest.
  Candidates for the identity signal: the kernel command line (a
  `liken.machine=` parameter the bootloader owns), a hardware fact (MAC
  address, DMI serial, TPM identity), or a config partition per machine.
  Related: where do many manifests live — many files in one image, or
  fetched by identity at boot? The cluster-growth milestone's claim flow
  is the current best candidate for the answer.
* **Static networking.** `spec.network` today only picks an interface and
  assumes DHCP. Static addressing needs address/gateway/nameserver fields
  and a story for machines that must come up when no DHCP exists.
