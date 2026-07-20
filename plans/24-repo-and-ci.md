# A real repository and CI builds

Milestone 24 — Done

liken today is a checkout that builds itself for one person. The
first step toward becoming a public project is unglamorous: a public
repository, and continuous integration that proves every commit builds
on a machine other than the author's.

The interesting part is what "builds" means here, because this repo is
not one program. A full build fetches and verifies every vendored
input (kernel, k3s, xtables, the trust bundle), builds two domains
from pinned source inside pinned containers (open-iscsi, nfs-utils),
compiles the Go pieces and runs their tests, and assembles a bootable
image. CI should do all of this, plus the one thing that unit tests
cannot claim to prove: boot the assembled image. The lab already
anticipates this. Its QEMU flags prefer KVM and fall back to pure
emulation, specifically because CI runners rarely offer
virtualization, and run-once exists so that a boot can be a bounded,
machine-readable artifact. A smoke boot under TCG, checked by reading
the serial console, is the necessary minimum. Whether CI can go further,
for example forming a single-node cluster, depends on how much time
each runner allows.

Some of this design is now settled. The repo is public at
github.com/liken-sh/liken, and CI runs on GitHub Actions. The checks
workflow (.github/workflows/checks.yaml) runs the repo's own
pre-commit hooks, including the unit tests and the coverage ratchet,
on every push to main and every pull request. It runs them through
prek, the same tool a developer uses for a local commit.

The build workflow (.github/workflows/build.yaml) runs `make all`:
every vendored input is fetched and verified, the Go programs are
compiled, and the image is assembled. It uses one cache for each
vendored domain, keyed on the same prerequisites that the domain's
Makefile rule declares, so that a pin bump rebuilds exactly that
domain from a cold cache. The workflow then boots the result. `make
smoke-uefi` (dev-cluster/smoke-uefi.sh) starts node-1 from blank disks
under KVM (the runners expose /dev/kvm), and the run passes when the
node reports the Ready state over the cluster's API. The workflow
reads this API through the leader's forwarded port, using the
offline-minted admin kubeconfig. The serial console log uploads as an
artifact on every run, whether the run passes or fails.

The plan originally called for growing the smoke drill from one
machine to three: a founding leader plus two followers joining over
the multicast cluster segment. This requirement has been dropped. The
single-node boot already proves what CI needs to prove here: the
assembled image boots and forms a working cluster on a machine that
nobody set up by hand. The lab's own drills already exercise
multi-node behavior constantly. If a regression ever slips through
that only a three-node CI run would have caught, that will be the
evidence needed to revisit this decision.
