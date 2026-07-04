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
2. [ ] Init starts k3s and nothing else — and discover every host dependency
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
3. [ ] Design the public bootstrapping story: today the identity bundle is
       minted by make in a private checkout, but a released OS needs a way
       for anyone to mint theirs — an installer step, a tiny CLI, or a
       documented openssl recipe.
4. [ ] Bake in Flux bootstrap, so the machine converges to its repo from
       first boot.
5. [ ] The mastery tier: A/B image upgrades, UKIs, dm-verity, secure boot,
       TPM-sealed secrets.

# Known hacks to unwind

Fixes from the boot-to-k3s work that encode knowledge k3s never promised
us; each works today and is guarded by the version pin + `make run-once`.

* [ ] Init's PATH hardcodes k3s's internal layout
  (`/var/lib/rancher/k3s/data/current/bin` and `bin/aux`) — probably
  redundant since dropping `prefer-bundled-bin`; test removing it.
* [ ] The `/sbin/iptables` dangling symlinks point into k3s's unpacked
  bundle. Proper fix: vendor our own static xtables binaries with a
  pinned, verified fetch like everything else, so `/sbin/iptables` is a
  real file with no coupling to k3s internals.
* [ ] File the upstream k3s issue: its bundled iptables entrypoint is a
  `#!/bin/sh` detection script, which breaks any host without a shell.
* [x] switch_root onto a plain tmpfs early in boot, the way k3OS did,
  so the root filesystem is an ordinary measurable mount instead of the
  kernel's magic initramfs rootfs. This let us drop
  `local-storage-capacity-isolation=false` entirely and silenced
  kubelet's recurring filesystem-stat errors — kubelet now measures /
  like any other machine's.

# Open problems

Questions we know we owe answers, without pretending to have them yet:

* **Which machine am I?** One image may boot many machines, so something
  has to definitively identify a machine and select its Machine manifest.
  Candidates for the identity signal: the kernel command line (a
  `liken.machine=` parameter the bootloader owns), a hardware fact (MAC
  address, DMI serial, TPM identity), or a config partition per machine.
  Related: where do many manifests live — many files in one image, or
  fetched by identity at boot?
* **Static networking.** `spec.network` today only picks an interface and
  assumes DHCP. Static addressing needs address/gateway/nameserver fields
  and a story for machines that must come up when no DHCP exists.
* **Volumes need a disk to exist first.** The Machine spec deliberately
  says nothing about storage yet, because there are no block devices to
  govern — the whole machine is RAM. Attach a real disk to `make run`
  (a qcow2, giving `/var/lib/rancher` persistence so images stop
  re-importing every boot), then design `spec.volumes` against it, with
  `status.hardware.blockDevices` as the read side.
* **Cluster membership.** A `kind: Cluster` manifest carrying what a
  machine needs to form or join a cluster: the k3s join token (or a
  reference to it — a token in plain YAML is a secrets problem), the
  server URL to join, and which machines are servers vs. agents. How it
  relates to Machine (embedded? referenced by name?) is undecided.
