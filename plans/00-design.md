# The liken design

liken is an operating system distribution for machines that run only
Kubernetes. A person who reads the repository from top to bottom learns
how a Linux system boots and how Kubernetes takes control after that.
This document is the overview. Each numbered document covers one
milestone in full: the design, the reasons for it, and the results from
the lab. The documents follow the order of the work, and that order is
important. Storage came before editable specs. Editable specs came
before multiple leaders. Visibility came before automated rollouts. Each
milestone depends on the one before it.

## The operating system is a kernel and a system image

liken vendors the kernel from Ubuntu's mainline builds, with no changes
to the upstream code. A small boot archive, boot.cpio, carries init, the
modules that the early boot needs, and mke2fs for an install boot. The
system image, liken.sqfs, is a read-only squashfs that carries k3s and
everything that k3s needs from a host. Init loop-mounts that image from
the boot slot, or from RAM when the boot loader delivered it. A third archive, microcode.cpio, carries CPU microcode
ahead of the others, because the kernel looks for microcode before it
decompresses anything else.

liken has no package manager, no shell, and no SSH. The serial console
reports what the machine does. Every other operation goes through the
Kubernetes API.

A machine installs itself onto A/B boot slots. To upgrade, a machine
writes the new version into the slot it does not run from, and boots
that slot once as a trial. If the trial fails, the machine falls back to
the slot it ran before. A UEFI machine falls back through the firmware's
boot-entry variables, and a BIOS machine falls back through what GRUB
reads.

## Two planes and no third

Machine-plane work runs as goroutines in init. Workload-plane software
runs under k3s. k3s is the only child process that init supervises. A
concern belongs to the machine plane only if k3s needs it to exist:
storage, network, time, and identity files. The cluster runs everything
that the cluster can host. The operating system has three in-cluster
components of its own: the machine operator, which reconciles machines,
the cluster operator, which reconciles the cluster, and the relays,
which turn host logs into pod logs. The image includes each one as
hand-assembled OCI tarballs, and they deploy through the k3s
auto-manifests directory. Thus the operating system never needs a
registry pull to run.

## The Kubernetes API is the machine API

Two custom resources describe everything. A Machine resource describes
one computer: its interfaces, its disks (declared by purpose, not by
path), its sysctls, and its reboot policy. The Machine status reports
what the machine observes about itself. The status uses the same phases
and conditions that Kubernetes uses for Pods and Nodes. A Cluster
resource describes what the machines form together. It lists which
machines run control planes. A machine is a leader when its name is in
spec.leaders, and no field declares a role for each machine. The Cluster
also declares the address plan that every node must agree on, where time
comes from, and how many machines can be down at the same time. It
declares the release catalog and the target version for the fleet. Last,
it records whether liken made the cluster datastore, or the cluster
adopted a datastore that liken did not make.

People, or a git repository, declare specs. Machines observe and report
status. The API server keeps this split. CEL admission rules refuse an
edit that can never take effect. Nobody copies data off a running
machine. Machines have no shell, so there is no way to copy data off
them.

## Identity is an input

The image carries the certificate authorities and the join token for the
cluster. Someone mints these offline before any machine boots, or
imports them from the servers of an existing cluster during adoption.
Because the image carries the identity, machines built from the same
image belong to the same cluster. The build computes an operator
kubeconfig offline from the client CA. The join token holds a hash of
the server CA. Thus a machine that joins verifies the cluster before it
sends its own secret.

## Change converges by reboot

Every kind of change follows one lifecycle. First, the machine finds the
difference between the spec and what this boot ran. Next, it writes the
change durably to its own disk. Then it reboots into the staged change
as a trial. If the boot is good, the machine promotes the change. If the
boot fails, the machine falls back to the last proven state. Machine
manifests, the cluster document, and operating system releases are three
staging stores. All three use the same four files and the same rules.
The fleet applies staged changes without supervision. A machine that is
ready publishes that it waits. An elected leader gives reboot turns one
at a time, workers first. The leader never lets more than one
control-plane member be down at the same time, because the loss of two
members can break the etcd majority.

## The lab

dev-cluster/ is the deployment that this repository develops against. It
uses QEMU guests with real UEFI firmware, and SeaBIOS guests for the
BIOS path, blank disks that the machines
claim and format themselves, and a multicast socket in place of a
switch. Each milestone ends with a run of its design in the lab,
including the failure paths: clocks set years wrong, power cuts during
install, and releases built to panic. A fallback is proven only after
the lab has caused it.

## Reading order

The documents are numbered in the order in which the work was planned.
A few milestones closed out of that order: 18 closed before 17, which
builds on it, and 24 closed before 22. The
completed milestones are in plans/completed/. Milestone 39 was built and
then backed out, and it is in plans/rejected/. The milestones that are
still open stay next to this file. The questions that liken owes an
answer to are in plans/open-problems/, one document each and no
numbers. README.md, next to this file, is the index. It also holds the
deferred hardening tier.
