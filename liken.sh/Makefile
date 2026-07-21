# Building the liken.sh deployment: the artifacts that turn a Linode
# instance into this cluster's founding leader.
#
# The operating system here comes from the public release channel,
# never from this repo's build tree. This deployment exists to use
# the channel the way any fleet anywhere does: install once from a
# published release, then upgrade from published releases forever
# after. Composing its media starts with a verified download:
#
#     make RELEASE=2026.07.14-001
#     make RELEASE=2026.07.14-001 DIGEST=sha256:<hex>
#     make latest
#
# RELEASE names a published version (a pasted tag's leading v is
# accepted). DIGEST, printed in the release workflow's run summary,
# pins the release document itself, the same digest that a Cluster
# commits to its catalog. This closes the trust chain end to end. If
# DIGEST is left empty, the artifacts are still verified against the
# document, but the document is not verified against a pin. `make
# latest` composes whatever the channel currently names as newest.
# The one piece of the local build that this Makefile still assumes
# is the toolkit (`make -C ../cli`), which does the fetching and
# composing. Every byte that reaches the disk comes from the verified
# download.
#
# What differs from the dev cluster is the destination. The dev
# cluster boots QEMU guests directly. This deployment produces a
# complete installed disk image, because the machine it targets is a
# Linode whose disk this build can write, but whose console it can
# only watch. The install still happens the ordinary way: the
# installer boots and runs exactly as it would on hardware, but it
# runs here, in a local QEMU guest, against a raw disk file. The
# build then copies the written disk byte for byte onto the machine's
# system disk.
#
# The install runs under the same firmware that Linode boots with:
# BIOS (SeaBIOS, QEMU's default; asking for no firmware is how you
# ask for it). The Machine manifest declares the biosBoot and
# bootHome roles, so the installer lays the whole GRUB boot path down
# itself: the MBR's boot code, the core image in its raw partition,
# and the config and environment block on the boot home. The disk
# that comes out boots wherever BIOS firmware runs it, with nothing
# planted by hand and no root privileges anywhere in the build.

CLI := ../cli/dist/liken

# Where releases come from. The public channel is the default. A
# locally served channel (`liken serve`, at
# http://localhost:8017/releases) stands in for it during
# development, and exercises the same download path end to end,
# instead of keeping a second build graph around.
CHANNEL ?= https://releases.liken.sh
DIGEST ?=

# This strips the leading v, so a pasted tag can name its release.
# `override` is needed because command-line definitions (make
# RELEASE=...) silently override plain assignments, and the command
# line is exactly where RELEASE arrives from.
override RELEASE := $(patsubst v%,%,$(strip $(RELEASE)))

# The system image's size: 3 GiB holds both boot slots, the machine's
# state, and its scratch space, with room left over. Keeping it small
# keeps the system disk disposable, because everything durable lives
# on the machine's data disk, which is claimed once and never
# reinstalled. The image may land on a disk slightly larger than
# itself, because cloud providers' advertised sizes do not land on
# exact byte counts. A GPT records where its disk ends, and on first
# boot liken notices a disk larger than its table claims, and
# rewrites the table to the real end.
SYSTEM_DISK_BYTES := 3221225472

# A scratch stand-in for the machine's data disk, present only
# during the local install, so that storage reconciliation finds
# every declared device. Its contents never travel: the real data
# disk starts blank on Linode, and the machine claims it on first
# boot.
DATA_SCRATCH_BYTES := 23622320128

all: image/disk.img.gz

# The downloaded release, cached under a version-keyed path, the same
# way the vendored domains cache their dist trees. A published
# release is immutable, and fetch writes release.yaml only after
# every artifact is verified. Because of this, an existing
# release.yaml means the whole directory is good, and the network is
# never asked twice for the same version. An interrupted download
# never looks finished to Make. Rerunning the fetch verifies what
# landed and fetches only what is missing. The toolkit is an
# order-only prerequisite for the usual reason: a rebuilt CLI must
# never make Make think that a verified download is stale.
channel/$(RELEASE)/release.yaml: | $(CLI)
	@test -n "$(RELEASE)" || { echo "name a release: make RELEASE=<version>, or make latest"; exit 1; }
	$(CLI) fetch $(if $(DIGEST),-digest $(DIGEST)) $(CHANNEL) $(RELEASE) channel

# Composing whatever the channel currently names as newest. This
# target resolves the advisory channel document to a concrete
# version, then re-enters make under that version's name. This keeps
# the cache version-keyed, so "latest" never becomes a path.
latest:
	$(MAKE) RELEASE=$$(curl -fsSL $(CHANNEL)/channel.yaml | awk '/^latest:/ {print $$2}')

# The cluster's identity: certificate authorities and the join
# token, minted before any machine boots. The CLI is an order-only
# prerequisite for the same reason as above: a rebuilt toolkit must
# never make Make think that this cluster's identity is stale.
identity/tls/server-ca.crt identity/token &: | $(CLI)
	$(CLI) mint identity

identity: identity/tls/server-ca.crt identity/token

# An operator's admin credential, computed offline from the client
# CA. The endpoint inside points at the cluster segment. Reaching it
# from outside means overriding the server, and the drills do this
# with kubectl's --server and --tls-server-name flags.
kubeconfig: identity/tls/server-ca.crt $(CLI)
	$(CLI) kubeconfig identity

# The deployment layer: this cluster's manifests and identity,
# packed as a cpio archive that every boot loads over the generic
# OS. Kernel modules are not part of it: the release's system image
# carries the kernel's whole module tree, and spec.modules loads
# from that tree at boot.
image/deployment.cpio: $(CLI) cluster.yaml $(wildcard machines/*.yaml) \
		identity/tls/server-ca.crt identity/token
	@mkdir -p image
	$(CLI) layer . identity $@

# The install image: the downloaded release composed with this
# deployment's layer, carrying the payload that the installer
# verifies and copies onto the machine's disk. media re-verifies
# every artifact against the release document before packing, so
# nothing unverified can reach a disk, even if someone tampered with
# the cache.
image/install.cpio: channel/$(RELEASE)/release.yaml image/deployment.cpio $(CLI)
	$(CLI) media channel/$(RELEASE) image/deployment.cpio $@

# The installed disk, produced by really performing the install.
# QEMU boots the installer under SeaBIOS, with the raw image attached
# as a virtio-scsi disk. This matches Linode's controller, so the
# disk claims itself at the /dev/sda that the Machine manifest names.
# The kernel that runs the install is the release's own vmlinuz,
# exactly what the machine will boot. The installer partitions,
# copies the release into slot A, lays down the GRUB boot path that
# the declared roles ask for, and powers off. -no-reboot turns that
# power-off into QEMU's exit. The console log lands next to the image
# for reading afterward.
#
# The guest gets 4 GB because the install image carries the whole OS
# in its initrd, in RAM, and under SeaBIOS the kernel reports almost
# all of the memory below a large initrd as reserved until the
# unpack finishes (the dev-cluster Makefile documents the observed
# numbers). The machine that boots the finished disk still needs
# only its 1 GB: the OS runs from the disk there.
#
# scsi_mod.scan=sync makes the kernel probe the two SCSI disks in
# attachment order, so /dev/sda is always the system disk that the
# Machine manifest names. The kernel's default asynchronous scan can
# finish out of order, and a blank-disk claim is the one moment when
# device paths matter: after the claim, the GPT partition names are
# the identity, and enumeration order stops mattering.
#
# Shipping this image is a plain Linode image deploy. Linode's
# deploys copy data byte for byte, end to end, boot code included
# (Linode once zeroed the MBR's first 440 bytes, a hazard that the
# README records). The machine guards those bytes itself regardless,
# and heals them from its proven slot's artifacts on every boot and
# before every reboot.
image/disk.img: image/install.cpio channel/$(RELEASE)/release.yaml
	rm -f $@ image/data-scratch.img
	truncate -s $(SYSTEM_DISK_BYTES) $@
	truncate -s $(DATA_SCRATCH_BYTES) image/data-scratch.img
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-cpu max \
		-smp 2 \
		-m 4096 \
		-kernel channel/$(RELEASE)/vmlinuz -initrd image/install.cpio \
		-append "console=ttyS0 scsi_mod.scan=sync rdinit=/liken liken.machine=node-1 liken.install" \
		-display none \
		-serial file:image/install.log \
		-monitor none \
		-no-reboot \
		-netdev user,id=uplink \
		-device virtio-net-pci,netdev=uplink \
		-netdev user,id=cluster \
		-device virtio-net-pci,netdev=cluster \
		-device virtio-scsi-pci \
		-drive file=$@,format=raw,if=none,id=system \
		-device scsi-hd,drive=system,serial=liken-node-1-system \
		-drive file=image/data-scratch.img,format=raw,if=none,id=data \
		-device scsi-hd,drive=data,serial=liken-node-1-data
	rm -f image/data-scratch.img
	grep -q 'install failed' image/install.log && { tail -5 image/install.log; rm -f $@; exit 1; } || true
	grep -q 'install complete' image/install.log || { tail -5 image/install.log; rm -f $@; exit 1; }

# The disk, made small for transport: all but about 1GB of the image
# is zeros, which compress to almost nothing. This uses gzip, rather
# than a better format, because gzip is the one format that Linode's
# image upload accepts.
image/disk.img.gz: image/disk.img
	gzip --keep --force $<

# ---------------------------------------------------------------------------
# The website (website/): the manifests that serve the site. The
# site's content lives in the docs domain at the repository root, and
# ships as a container image: CI builds it and repoints the
# Deployment on every push that touches it
# (.github/workflows/docs.yaml). The resources here land in the
# cluster's own datastore on the data disk, so they survive reboots
# and release rolls. website/README.md tells the whole deploy story,
# including the one-time cost of the Traefik TLS overlay, and why DNS
# must resolve before the first deploy.

# The manifest deploy, using the operator's admin credential. CI's
# credential deliberately cannot touch manifests, so changes to them
# always ship from a workstation, through this target. The namespace
# is applied by name first, because every other resource lands inside
# it, and `kubectl apply` walks a directory in filename order.
website:
	./kubectl apply -f website/manifests/namespace.yaml
	./kubectl apply -f website/manifests/

# clean removes the assembled artifacts, but leaves channel/ alone.
# Those files are verified downloads, gigabytes that redownload
# identically, and cleaning scratch files should not cost the network
# anything.
clean:
	rm -rf image

.PHONY: all latest identity kubeconfig website clean
