# The design, in one place

liken is an operating system distribution for machines that run only
Kubernetes. We write the repo so people can read it like a course: it
teaches how a Linux system boots and how Kubernetes takes over from
there. This document gives the overview. Each numbered document next
to it covers one milestone in full: the design, the reasoning, and
what the lab showed when it ran. The documents follow the order of
the work, and the order matters. Storage came before editable specs.
Editable specs came before multiple leaders. Visibility came before
automated rollouts. Each milestone depends on the one before it.

## The operating system is two files

The whole OS is two files: a kernel and an initramfs. liken vendors
the kernel from Ubuntu's mainline builds, with no changes to the
upstream code. The initramfs, liken.cpio, carries init, k3s, and
everything k3s assumes a host provides. liken has no package manager,
no shell, and no SSH. The serial console reports what the machine is
doing. Everything else happens through the Kubernetes API. Machines
install themselves onto A/B boot slots. To upgrade, a machine writes
the new version into the slot it is not running from, boots it once
as a trial, and falls back through UEFI firmware's own boot-entry
mechanism if the boot fails.

## Two planes and no third

Machine-plane concerns run as goroutines in init. Workload-plane
software runs under k3s. k3s is the only child process that init
supervises. A concern belongs in the machine plane only when k3s
needs it to exist: storage, network, time, and identity files.
Anything the cluster can host on its own runs in the cluster instead.
The OS has two in-cluster components of its own: the operator, which
reconciles machines, and the relays, which turn host logs into pod
logs. The image includes both as hand-assembled OCI tarballs, which deploy
through k3s's auto-manifests directory. Because of this, running the
OS never requires a registry pull.

## The Kubernetes API is the machine API

Two custom resources describe everything. A Machine resource
describes one computer: its interfaces, its disks (declared by
purpose, not by path), its sysctls, and its reboot policy. The
Machine's status reports what the machine observes about itself.
Status uses the same phases and conditions that Kubernetes uses for
Pods and Nodes. A Cluster resource describes what the machines form
together. It lists which machines run control planes (a machine is a
leader when its name appears in spec.leaders; no field declares a
role per machine). It also declares the address plan every node must
agree on, where time comes from, and how many machines may be down at
once. It declares the release catalog and the fleet's target version.
Last, it records whether liken created the cluster's datastore, or
the cluster adopted it from a cluster liken did not create.

People, or a git repository, declare specs. Machines observe and
report status. The API server enforces this split. CEL admission
rules refuse edits that could never take effect. No one ever copies
data off a running machine. Machines have no shell, so there is no
way to copy data off them anyway.

## Identity is an input

The image carries the cluster's certificate authorities and join
token. Someone mints these offline before any machine boots, or
imports them from an existing cluster's servers when adopting a
cluster. Because the image carries identity, machines built from the
same image belong to the same cluster. The build computes an
operator's kubeconfig offline from the client CA. The join token
embeds a hash of the server CA. Because of this, a joining machine
verifies the cluster before it presents its own secret.

## Change converges by reboot

Every kind of change follows one lifecycle. First, the machine
detects drift against what this boot actually ran. Next, it stages
the change durably on its own disk. Then it reboots into the staged
change as a trial. If the boot succeeds, the machine promotes the
change. If the boot fails, the machine falls back to the last proven
state. Machine manifests, the cluster document, and OS releases are
three staging stores. All three use the same four files and the same
rules. The fleet applies staged changes without supervision. A
machine that is ready publishes that it is waiting. An elected leader
grants reboot turns one at a time, workers first. The leader never
lets more than one control-plane member be down at once, because
losing two risks etcd's majority.

## The lab

dev-cluster/ is the deployment this repo develops against. It uses
QEMU guests with real UEFI firmware, blank disks that the machines
claim and format themselves, and a multicast socket in place of a
switch. Every milestone ends by running its design in the lab,
including the failure paths: clocks set years wrong, power cuts
during install, and releases built to panic. A fallback counts as
proven only after the lab has made it happen.

## Reading order

The documents in this directory are numbered in the order the work
happened. Documents 01 through 16 cover work that is built and
proven; 11 and 14 remain open explorations. Documents 17 through 21
cover capabilities that a survey of a real deployment's workloads
showed the OS still needs. Document 22 covers the question of whether
to release liken to the public. README.md, next to this file, is the
index. It holds the shorter-term notes: the deferred hardening tier
and the open problems.
