# A real repository and CI builds

Milestone 24. Completed. liken has a public repository and continuous
integration that builds every commit and boots the result.

The repository is public at github.com/liken-sh/liken, and CI runs on
GitHub Actions. CI proves that every commit builds on a machine other
than the author's.

A build here is not one program. A full build fetches and verifies
every vendored input (the kernel, k3s, xtables, the trust bundle),
builds two domains from pinned source inside pinned containers
(open-iscsi, nfs-utils), compiles the Go pieces and runs their tests,
and assembles a bootable image. CI does all of this, and it also does
the one thing that unit tests cannot prove: it boots the assembled
image. The lab was already prepared for this. Its QEMU flags prefer KVM
and fall back to pure emulation, because CI runners rarely offer
virtualization. run-once makes a boot a bounded, machine-readable
artifact. A smoke boot under TCG, checked by a read of the serial
console, is the necessary minimum. Whether CI can go further, for
example forming a single-node cluster, depends on how much time each
runner allows.

The checks workflow (.github/workflows/checks.yaml) runs the
repository's own pre-commit hooks, including the unit tests and the
coverage ratchet, on every push to main and every pull request. It runs
them through prek, the same tool a developer uses for a local commit.

The build workflow (.github/workflows/build.yaml) runs `make all`. It
fetches and verifies every vendored input, compiles the Go programs,
and assembles the image. It uses one cache for each vendored domain,
keyed on the same prerequisites that the domain's Makefile rule
declares, so that a pin bump rebuilds exactly that domain from a cold
cache. The workflow then boots the result. `make smoke-uefi`
(dev-cluster/smoke-uefi.sh) starts node-1 from blank disks under KVM,
because the runners expose /dev/kvm. The run passes when the node
reports the Ready state over the cluster's API. The workflow reads this
API through the leader's forwarded port, with the offline-minted admin
kubeconfig. The serial console log uploads as an artifact on every run,
whether the run passes or fails.

The plan first called for growing the smoke drill from one machine to
three: a founding leader plus two followers joining over the multicast
cluster segment. This requirement is dropped. The single-node boot
proves what CI must prove here: the assembled image boots and forms a
working cluster on a machine that nobody set up by hand. The lab's own
drills exercise multi-node behavior constantly. If a regression slips
through that only a three-node CI run would have caught, that is the
evidence needed to revisit this decision.
