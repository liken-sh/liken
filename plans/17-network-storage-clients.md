# Host clients for network storage: iSCSI and NFS

Milestone 17 — Not started

liken's storage design covers the machine's own disks, but real
workloads mount network storage through CSI drivers, and a CSI node
plugin depends on the host for the actual clients: iscsiadm and
iscsid to attach SAN block devices, mount.nfs for NAS shares, and the
kernel modules under both. The driver's pods can carry their own
controllers, but the mount happens in the host's namespaces with the
host's tools. Without them, every network-backed PV on a liken node
stays Pending.

The work is the vendoring pattern this repo already uses (e2fsprogs,
xtables: pinned versions, static binaries) plus entries in the module
list. Prove it in the lab by running a real CSI driver's node plugin
on a liken machine and mounting one block volume and one file volume
through it.
