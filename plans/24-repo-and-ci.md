# A real repository and CI builds

Milestone 24 — Not started

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

Open questions, deliberately unanswered here: which forge hosts it
(and whether CI is the forge's or something self-hosted); how caching
keeps the vendored fetches and container builds from making every CI
run a cold build; and whether CI should also prove the checks the
repo already runs locally through prek, which it almost certainly
should.
