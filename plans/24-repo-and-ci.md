# A real repository and CI builds

Milestone 24 — In progress: the repo is public and the checks run in
CI; the build and smoke boot remain

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

What remains is the build half: `make all` in CI with per-domain
caches keyed on the VERSION pins, and the smoke boot. GitHub's Linux
runners expose /dev/kvm, so the boot can be a real KVM boot of the
assembled image via run-once, with the serial console as the bounded
artifact. The open question is the success signal — QEMU exiting
isn't success, since a crashed k3s also exits; the honest minimum is
probably the node reaching Ready with the operators alive, read from
the console log.
