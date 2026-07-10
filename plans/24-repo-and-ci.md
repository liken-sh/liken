# A real repository and CI builds

Milestone 24 — In progress: the repo is public, and CI runs the
checks, the build, and a single-node smoke boot; the three-node
drill remains

liken today is a checkout that builds itself for one person. The
first step toward being a public project is the unglamorous one: a
public repository, and continuous integration that proves every
commit builds without the author's machine.

The interesting part is what "builds" means here, because this repo
is not one program. A full build fetches and verifies every vendored
input (kernel, k3s, xtables, the trust bundle), builds two domains
from pinned source inside pinned containers (open-iscsi, nfs-utils),
compiles the Go pieces and runs their tests, and assembles a bootable
image. CI should do all of it, plus the one thing unit tests can't
claim: boot the assembled image. The lab already anticipates this —
the QEMU flags prefer KVM and fall back to pure emulation precisely
because CI runners rarely offer virtualization, and run-once exists
so a boot can be a bounded, machine-readable artifact. A smoke boot
under TCG, checked by reading the serial console, is the honest
minimum; whether CI can go further (a single-node cluster forming) is
a question of runner patience.

Some of this is now settled. The repo is public at
github.com/liken-sh/liken and CI is GitHub Actions: the checks
workflow (.github/workflows/checks.yaml) runs the repo's own
pre-commit hooks — including the unit tests and the coverage ratchet —
on every push to main and every pull request, via prek, the same way
a developer's commit does.

The build workflow (.github/workflows/build.yaml) runs `make all` —
every vendored input fetched and verified, the Go programs compiled,
the image assembled — with one cache per vendored domain, keyed on
the same prerequisites the domain's Makefile rule declares, so a pin
bump rebuilds exactly that domain cold. Then it boots the result:
`make smoke` (dev-cluster/smoke.sh) starts node-1 from blank disks
under KVM (the runners expose /dev/kvm) and passes when the node
reports Ready over the cluster's API, read through the leader's
forwarded port with the offline-minted admin kubeconfig. The serial
console uploads as an artifact on every run, pass or fail.

What remains is growing the smoke drill from one machine to three:
the founding leader plus two followers joining over the multicast
cluster segment, with the pass condition all three nodes Ready.
