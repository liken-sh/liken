# The lab's storage server: the fixture the iscsi and nfs features
# and the registry mirror are drilled against.
#
# Milestone 17 made liken a client of network storage, and a client
# needs a server. This guest is that server, and it is deliberately
# not a liken machine: it runs stock Debian, because the point of the
# drill is liken's side of the wire, and the server side should be
# the boring reference implementation everyone else runs — the kernel
# NFS server, a standard iSCSI target, and CNCF distribution, the
# reference container registry. It sits on the same multicast segment
# as the fleet, at 10.10.0.100, serving one NFSv4 export, one iSCSI
# LUN, and one authenticated pull-through registry: the way the NFS
# export answers on 2049 for the nfs feature, the registry answers on
# 5000 for spec.registries and the registry-credentials Secret
# (milestone 20).
#
# There is no installer. Debian publishes cloud images: generic qcow2
# disks that boot ready-made and configure themselves on first boot
# from a cloud-init seed. The seed here is a small ISO (the NoCloud
# datasource: a volume labeled "cidata" carrying user-data, meta-data,
# and network-config), and seed/ is the server's entire
# configuration — addresses, packages, the export, the target. Wipe
# the guest (make clean) and the next boot rebuilds the identical
# server from the seed alone.
#
# Run it in its own terminal, like any node:
#
#   make -C dev-cluster storage
#
# It participates in nothing: no cluster document, no operator, no
# manifests. The fleet only ever sees it as an address that answers
# on ports 2049 (NFS) and 3260 (iSCSI).

VERSION := $(strip $(file <VERSION))
BASE := cache/$(VERSION)/debian-13-genericcloud-amd64-$(VERSION).qcow2

# MACs continue the lab's scheme (52:54:00 is QEMU's prefix, 4c:4b/4c
# read "LK"); the machines take 01–05, the storage server takes 10.
UPLINK_MAC := 52:54:00:4c:4b:10
CLUSTER_MAC := 52:54:00:4c:4c:10

$(BASE): VERSION fetch.sh
	./fetch.sh

# The host's door into the server: an SSH keypair minted per
# checkout, never committed, the same posture as the lab's CAs and
# join token (see ../identity). The public half is appended to the
# seed's user-data below, so the committed seed carries no identity
# at all, and any checkout that builds the fixture can operate it.
guest/ssh guest/ssh.pub &:
	mkdir -p guest
	ssh-keygen -q -t ed25519 -N "" -C liken-lab-storage -f guest/ssh

# The seed's user-data is the committed file plus this checkout's
# public key; ssh_authorized_keys lands on the image's default user
# (debian), who holds passwordless sudo.
guest/user-data: seed/user-data guest/ssh.pub
	cat seed/user-data > $@
	printf 'ssh_authorized_keys:\n  - %s\n' "$$(cat guest/ssh.pub)" >> $@

# The seed ISO. xorriso in its mkisofs guise builds it from the
# assembled user-data and the two files in seed/; the "cidata" volume
# label is how cloud-init's NoCloud datasource recognizes the disk as
# its seed. Rock Ridge extensions preserve the lowercase filenames
# cloud-init looks for.
guest/seed.iso: guest/user-data seed/meta-data seed/network-config
	xorriso -as mkisofs -quiet -volid cidata -joliet -rational-rock \
		-output $@ guest/user-data seed/meta-data seed/network-config

# The root disk: a copy-on-write overlay whose backing file is the
# fetched Debian image, so the pristine base is shared, never
# written, and the guest's own state stays in guest/ where make clean
# can factory-reset it. The overlay is created larger than the base;
# reads past the base return zeros, and cloud-init grows the root
# partition and filesystem into the extra space on first boot.
guest/root.qcow2: $(BASE)
	mkdir -p guest
	qemu-img create -f qcow2 -F qcow2 -b $(abspath $(BASE)) $@ 8G

# The payload disks, blank like every disk in this lab. The server
# formats the exports disk ext4 and serves it over NFS; the luns disk
# is never formatted by the server at all — it is handed whole to the
# iSCSI target as the LUN, and whatever filesystem it carries is the
# initiator's business, which is the entire idea of block storage.
guest/exports.qcow2:
	mkdir -p guest
	qemu-img create -f qcow2 $@ 8G

guest/luns.qcow2:
	mkdir -p guest
	qemu-img create -f qcow2 $@ 8G

# Boot the server. The flags are the same vocabulary as the machines'
# (documented flag by flag in ../Makefile); what differs is the
# firmware and the size. SeaBIOS, QEMU's default, is plenty — the
# Debian image boots under BIOS or UEFI, and nothing here exercises
# firmware behavior the way the machines' install path does. Two
# vCPUs and one gigabyte, because nfsd and tgtd together are lighter
# than a kubelet.
#
# The NICs come before the disks so PCI slot order keeps the uplink
# and cluster segment stable; the disks carry serial numbers so the
# seed can name them by identity (/dev/disk/by-id/virtio-storage-*)
# instead of by probe order. The seed rides an IDE CD drive, keeping
# it out of the virtio disk namespace entirely.
#
# The uplink's hostfwd is the operator's door: host port 2210,
# loopback only, forwards to the server's sshd, so drills can load
# fixtures and inspect the server without touching its serial
# console:
#
#   ssh -i guest/ssh -p 2210 debian@127.0.0.1
run: guest/root.qcow2 guest/exports.qcow2 guest/luns.qcow2 guest/seed.iso
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-cpu max \
		-smp 2 \
		-m 1024 \
		-display none \
		-serial stdio \
		-monitor none \
		-netdev user,id=uplink,hostfwd=tcp:127.0.0.1:2210-:22 \
		-device virtio-net-pci,netdev=uplink,mac=$(UPLINK_MAC) \
		-netdev socket,id=cluster,mcast=239.192.76.75:26443 \
		-device virtio-net-pci,netdev=cluster,mac=$(CLUSTER_MAC) \
		-drive file=guest/root.qcow2,format=qcow2,if=none,id=root \
		-device virtio-blk-pci,drive=root,serial=storage-root \
		-drive file=guest/exports.qcow2,format=qcow2,if=none,id=exports \
		-device virtio-blk-pci,drive=exports,serial=storage-exports \
		-drive file=guest/luns.qcow2,format=qcow2,if=none,id=luns \
		-device virtio-blk-pci,drive=luns,serial=storage-luns \
		-cdrom guest/seed.iso

.PHONY: run clean

# A factory reset: the server's disks and seed ISO go, the fetched
# base image stays cached. The next run rebuilds the same server —
# including reformatting the exports disk and re-serving a blank LUN,
# so anything the fleet stored here is gone, which is the right
# meaning for a lab fixture's clean.
clean:
	rm -rf guest
