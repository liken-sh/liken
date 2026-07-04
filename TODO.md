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
   2. [ ] Machine identity is an input, not an output: `make` mints a
          CA bundle (gitignored) and pre-seeds k3s's TLS directory in the
          image, so an operator's kubeconfig is computed offline — never
          copied off the machine. `make kubeconfig`, `--tls-san`, and a
          QEMU port-forward for `kubectl get nodes` from the host.
   3. [ ] The Kubernetes API is the machine API: OS-level reads and writes
          become a Machine CRD (facts in status, declared state in spec),
          reconciled by a small in-cluster liken operator. Init never
          talks to k3s; it writes facts to `/run/liken/` and the operator
          does the API half.
3. [ ] Design the public bootstrapping story: today the identity bundle is
       minted by make in a private checkout, but a released OS needs a way
       for anyone to mint theirs — an installer step, a tiny CLI, or a
       documented openssl recipe.
4. [ ] Bake in Flux bootstrap, so the machine converges to its repo from
       first boot.
5. [ ] The mastery tier: A/B image upgrades, UKIs, dm-verity, secure boot,
       TPM-sealed secrets.
