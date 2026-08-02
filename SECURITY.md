# Security policy

## How to report a vulnerability

Report a vulnerability privately, through GitHub's advisory form:
[Report a vulnerability](https://github.com/liken-sh/liken/security/advisories/new).
The report opens a draft advisory that only you and the maintainer can
see. Do not open a public issue for a vulnerability.

Expect an acknowledgment within a week. liken has one maintainer, so
there is no on-call rotation behind that number.

## What is in scope

liken's own code is in scope: the init, the `liken` CLI, the operators
that run on a machine, the build, and the release workflow. The release
channel at https://releases.liken.sh is also in scope, because machines
trust what they download from it.

A vulnerability in a component that liken redistributes (the kernel,
k3s, GRUB, and the rest that [`licensing/NOTICES.md`](licensing/NOTICES.md)
lists) belongs to that component's own project. Report it there. But if
liken pins a version with a known fix available, that pin is a liken
problem, and a report here is welcome.

## How fixes ship

liken has no backports and no patch branches. A fix ships as a new
release on the channel, and machines upgrade to it. The newest published
release is the supported one.
