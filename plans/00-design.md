# The design, in one place

liken is an operating system distribution for machines whose whole
job is Kubernetes, and it is written to be read: the repo doubles as
a course in how a Linux system boots and how Kubernetes takes over
from there. This document is the overview. Each numbered document
beside it covers one milestone in full: the design, the reasoning, and what
the lab showed when it ran. They are ordered the way the work
happened, and the order matters: storage came before editable specs,
editable specs before multiple leaders, visibility before automated
rollouts, because each depends on the one before it.

## The operating system is two files

The whole OS is a kernel (vendored from Ubuntu's mainline builds,
vanilla upstream code) and an initramfs, liken.cpio, that carries
init, k3s, and everything k3s assumes a host provides. There is no
package manager, no shell, and no SSH: the serial console reports
what the machine is doing, and everything else happens through the
Kubernetes API. Machines install themselves onto A/B boot slots and
upgrade by writing the new version into the slot they are not running
from, booting it once tentatively, and falling back through UEFI
firmware's own boot-entry mechanism if it fails.

## Two planes and no third

Machine-plane concerns are goroutines in init; workload-plane
software runs under k3s; k3s is the only child process init ever
supervises. A concern is admitted to the machine plane only when k3s
depends on it to exist (storage, network, time, identity files);
anything the cluster could host for itself runs in the cluster. The
OS's own in-cluster components, the operator that reconciles machines
and the relays that turn host logs into pod logs, ride the image as
hand-assembled OCI tarballs and deploy through k3s's auto-manifests
directory, so running the OS never requires a registry pull.

## The Kubernetes API is the machine API

Two custom resources describe everything. A Machine is one computer:
its interfaces, its disks (declared by purpose, not by path), its
sysctls, its reboot policy. Its status is what the machine observes
about itself, expressed as phases and conditions in the same grammar
Kubernetes uses for Pods and Nodes. A Cluster is what the machines
form together: which of them run control planes (a machine is a
leader when its name appears in spec.leaders; role is never declared
per machine), the address plan every node must agree on, where time
comes from, how many machines may be down at once, the release
catalog and the fleet's target version, and whether the cluster's
datastore was created by liken or adopted from a cluster liken didn't
create.

Specs are declared by people (or a git repository); status is
observed by machines; the API server enforces that split, and CEL
admission rules refuse edits that could never take effect. Nothing is
ever copied off a running machine, and since the machines have no
shell, there is no way to do so anyway.

## Identity is an input

The image carries the cluster's certificate authorities and join
token, minted offline before any machine boots, or imported from an
existing cluster's servers when adopting one. Because identity rides
the image, machines built from the same image belong to the same
cluster. An operator's kubeconfig is computed offline from the client
CA. The join token embeds a hash of the server CA, so a joining
machine verifies the cluster before presenting its secret.

## Change converges by reboot

Every kind of change follows one lifecycle: detect drift against what
this boot actually ran, stage the change durably on the machine's own
disk, reboot into it tentatively, and promote it once the boot
succeeds, falling back to the last proven state when it doesn't.
Machine manifests, the cluster document, and OS releases are three
staging stores with the same four files and the same rules. The fleet
applies staged changes without supervision: machines publish that
they are waiting, and an elected leader grants reboot turns one at a
time, workers first, with never more than one control-plane member
down, because losing two risks etcd's majority.

## The lab

dev-cluster/ is the deployment this repo develops against: QEMU
guests with real UEFI firmware, blank disks the machines claim and
format themselves, and a multicast socket standing in for a switch.
Every milestone ends by running its design there, including the
failure paths: clocks set years wrong, power cuts mid-install,
releases built to panic. A fallback only counts once the lab has
made it fire.

## Reading order

The documents in this directory are numbered in the order the work
happened. 01 through 16 are built and proven (11 and 14 remain open
explorations); 17 through 21 are capabilities that surveying a real
deployment's workloads showed the OS still needs, and 22 is the
question of releasing liken to the public at all. README.md beside
this file is the index and holds the shorter-term notes: the deferred
hardening tier and the open problems.
