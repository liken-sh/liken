# Building the liken.sh deployment: the artifacts that turn a Linode
# instance into this cluster's founding leader.
#
# The operating system here comes from the public release channel,
# never from this repo's build tree. This deployment exists to
# consume the channel the way any fleet anywhere does — install once
# from a published release, upgrade from published releases forever
# after — so composing its media starts with a verified download:
#
#     make RELEASE=2026.07.14-001
#     make RELEASE=2026.07.14-001 DIGEST=sha256:<hex>
#     make latest
#
# RELEASE names a published version (a pasted tag's leading v is
# tolerated). DIGEST, printed in the release workflow's run summary,
# pins the release document itself — the same digest a Cluster
# commits to its catalog — closing the trust chain end to end; left
# empty, the artifacts are still verified against the document, just
# not the document against a pin. `make latest` composes whatever
# the channel currently names newest. The one piece of the local
# build still assumed is the toolkit (`make -C ../cli`), which does
# the fetching and composing; every byte that reaches the disk comes
# from the verified download.
#
# What's different from the dev cluster is the destination. The dev
# cluster boots QEMU guests directly; this deployment produces a
# complete installed *disk image*, because the machine it targets is
# a Linode whose disk we can write but whose console we only watch.
# The install still happens the ordinary way — the installer boots
# and runs exactly as it would on hardware — but it runs here, in a
# local QEMU guest, against a raw disk file. The written disk is then
# copied byte-for-byte onto the machine's system disk.
#
# The install runs under the same firmware Linode boots with: BIOS
# (SeaBIOS, QEMU's default — asking for no firmware is how you ask
# for it). The Machine manifest declares the biosBoot and bootHome
# roles, so the installer lays the whole GRUB boot path down itself:
# the MBR's boot code, the core image in its raw partition, and the
# config and environment block on the boot home. The disk that comes
# out boots anywhere BIOS firmware runs it, with nothing planted by
# hand and no root privileges anywhere in the build.

CLI := ../cli/dist/liken

# Where releases come from. The public channel is the default; a
# locally served one (`liken serve`, at http://localhost:8017/releases)
# stands in for it during development, exercising the same download
# path end to end rather than keeping a second build graph around.
CHANNEL ?= https://releases.liken.sh
DIGEST ?=

# The leading v is stripped so a pasted tag names its release.
# `override` because command-line definitions (make RELEASE=...)
# silently ignore plain assignments, and the command line is exactly
# where RELEASE arrives from.
override RELEASE := $(patsubst v%,%,$(strip $(RELEASE)))

# The system image's size: 3 GiB holds both boot slots, the machine's
# state, and its scratch space, with room left over. Keeping it small
# keeps the system disk disposable — everything durable lives on the
# machine's data disk, which is claimed once and never reinstalled.
# The image may land on a disk slightly larger than itself (cloud
# providers' advertised sizes don't land on exact byte counts): a GPT
# records where its disk ends, and liken notices a disk larger than
# its table claims on first boot and rewrites the table to the real
# end.
SYSTEM_DISK_BYTES := 3221225472

# A scratch stand-in for the machine's data disk, present during the
# local install only so that storage reconciliation finds every
# declared device. Its contents never travel: the real data disk
# starts blank on Linode and the machine claims it on first boot.
DATA_SCRATCH_BYTES := 23622320128

all: image/disk.img.gz

# The downloaded release, cached under a version-keyed path the way
# the vendored domains cache their dist trees. A published release is
# immutable, and fetch writes release.yaml only after every artifact
# verified, so an existing release.yaml means the whole directory is
# good and the network is never asked twice for the same version. An
# interrupted download never looks finished to Make; rerunning
# verifies what landed and fetches only what's missing. The toolkit
# is order-only for the usual reason: a rebuilt CLI must never make
# Make think a verified download is stale.
channel/$(RELEASE)/release.yaml: | $(CLI)
	@test -n "$(RELEASE)" || { echo "name a release: make RELEASE=<version>, or make latest"; exit 1; }
	$(CLI) fetch $(if $(DIGEST),-digest $(DIGEST)) $(CHANNEL) $(RELEASE) channel

# Composing whatever the channel currently names newest: resolve the
# advisory channel document to a concrete version, then re-enter make
# under that name, so the cache stays version-keyed and "latest"
# never becomes a path.
latest:
	$(MAKE) RELEASE=$$(curl -fsSL $(CHANNEL)/channel.yaml | awk '/^latest:/ {print $$2}')

# The cluster's identity: certificate authorities and the join
# token, minted before any machine boots. The CLI is an order-only
# prerequisite for the same reason as at the root: a rebuilt toolkit
# must never make Make think this cluster's identity is stale.
identity/tls/server-ca.crt identity/token &: | $(CLI)
	$(CLI) mint identity

identity: identity/tls/server-ca.crt identity/token

# An operator's admin credential, computed offline from the client
# CA. The endpoint inside points at the cluster segment; reaching it
# from outside means overriding the server, which the drills do with
# kubectl's --server and --tls-server-name flags.
kubeconfig: identity/tls/server-ca.crt $(CLI)
	$(CLI) kubeconfig identity

# The deployment layer: this cluster's manifests, identity, and its
# machines' declared kernel modules, packed as the second cpio
# archive every boot loads over the generic OS. The kernel directory
# is `-` because node-1 declares no modules — and a release publishes
# vmlinuz but not the module tree, so a machine that one day declares
# modules will need the repo's kernel vendored at the version the
# release's components record.
image/deployment.cpio: $(CLI) cluster.yaml $(wildcard machines/*.yaml) \
		identity/tls/server-ca.crt identity/token
	@mkdir -p image
	$(CLI) layer . identity - $@

# The install image: the downloaded release composed with this
# deployment's layer, carrying the payload the installer verifies and
# copies onto the machine's disk. media re-verifies every artifact
# against the release document before packing, so nothing unverified
# can reach a disk even if the cache were tampered with.
image/install.cpio: channel/$(RELEASE)/release.yaml image/deployment.cpio $(CLI)
	$(CLI) media channel/$(RELEASE) image/deployment.cpio $@

# The installed disk, produced by really installing. QEMU boots the
# installer under SeaBIOS with the raw image attached as a
# virtio-scsi disk — Linode's controller, so the disk claims itself
# at the /dev/sda the Machine manifest names. The kernel that runs
# the install is the release's own vmlinuz, exactly what the machine
# will boot. The installer partitions, copies the release into slot
# A, lays down the GRUB boot path the declared roles ask for, and
# powers off; -no-reboot turns that power-off into QEMU's exit. The
# console log lands next to the image for reading afterward.
#
# Shipping this image is a plain Linode image deploy: their deploys
# measure byte-faithful end to end, boot code included (they once
# zeroed the MBR's first 440 bytes, a hazard the README records).
# The machine guards those bytes itself regardless, healing them
# from its proven slot's artifacts on every boot and before every
# reboot.
image/disk.img: image/install.cpio channel/$(RELEASE)/release.yaml
	rm -f $@ image/data-scratch.img
	truncate -s $(SYSTEM_DISK_BYTES) $@
	truncate -s $(DATA_SCRATCH_BYTES) image/data-scratch.img
	qemu-system-x86_64 \
		-accel kvm -accel tcg \
		-cpu max \
		-smp 2 \
		-m 2048 \
		-kernel channel/$(RELEASE)/vmlinuz -initrd image/install.cpio \
		-append "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.install" \
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

# The disk, travel-sized: all but ~1GB of the image is zeros, which
# compress to almost nothing. gzip rather than anything better
# because it is the one format Linode's image upload accepts.
image/disk.img.gz: image/disk.img
	gzip --keep --force $<

# ---------------------------------------------------------------------------
# The website (website/), deployed straight to the cluster's API: no
# image is rebuilt and no machine reboots to publish a page. The
# resources land in the cluster's own datastore on the data disk, so
# they ride out reboots and release rolls. website/README.md tells
# the whole deploy story — including the one-time cost of the Traefik
# TLS overlay and why DNS has to resolve before the first deploy.

# The full deploy: manifests, then content, with the operator's admin
# credential. The namespace is applied by name first because every
# other resource lands inside it and `kubectl apply` walks a
# directory in filename order.
website:
	./kubectl apply -f website/manifests/namespace.yaml
	./kubectl apply -f website/manifests/
	$(MAKE) website-content

# The content deploy, which is all CI is allowed to do: regenerate
# the page's ConfigMap from the HTML beside it, then restart and
# wait. kubelet would refresh the mounted page within a minute on its
# own; the restart gives the deploy a definite end, so a deployer can
# check the site serves what it just shipped.
website-content:
	./kubectl create configmap website --namespace website \
		--from-file website/index.html --dry-run=client -o yaml \
		| ./kubectl apply -f -
	./kubectl rollout restart --namespace website deployment/website
	./kubectl rollout status --namespace website deployment/website

# clean sweeps the assembled artifacts but leaves channel/ alone:
# those are verified downloads, gigabytes that redownload identically,
# and cleaning scratch shouldn't cost the network anything.
clean:
	rm -rf image

.PHONY: all latest identity kubeconfig website website-content clean
