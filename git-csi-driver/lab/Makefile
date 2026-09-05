# The lab: one liken machine in QEMU, booted from the public release
# channel, and a git forge on the host that the machine reaches over the
# user-mode network. Every drill in this repository runs here, because
# nothing is installed on a real cluster until a person has seen it
# work. The targets, disk names, firmware paths, and network devices
# copy liken's own dev-cluster/Makefile for one node.

NODE := node-1
GUEST := guests/$(NODE)
PIDFILE := $(GUEST)/qemu.pid
FORGE_PIDFILE := guests/forge.pid

# Where releases come from, and the version this checkout runs. fetch
# writes the version it resolved to channel/version, so every later
# target names the same release without asking the channel again.
SOURCE ?= https://releases.liken.sh
VERSION ?= $(shell cat channel/version 2>/dev/null)
RELEASE := channel/$(VERSION)
LIKEN := $(RELEASE)/liken

# The release's own toolkit fetches the release. The media format is
# versioned with the toolkit, so a release is always composed by the
# liken binary it ships, never by another version's.
BOOTSTRAP := channel/.liken

# The image the deploy target pins. The default is the newest build CI
# pushed, read from the package registry, so a drill after a push to
# main runs that push.
IMAGE ?= ghcr.io/liken-sh/git-csi-driver
IMAGE_TAG ?= $(shell gh api /orgs/liken-sh/packages/container/git-csi-driver/versions \
	--jq '[.[].metadata.container.tags[]] | map(select(test("^[0-9]"))) | .[0]' 2>/dev/null)

# Every wait in this file has a deadline, in seconds. These are them.
INSTALL_DEADLINE ?= 600
READY_DEADLINE ?= 300
DEPLOY_DEADLINE ?= 300
CSINODE_DEADLINE ?= 300
STORE_DEADLINE ?= 300

# The guest's shape, from liken's dev cluster. A disk boot runs in
# 1 GiB, the liken envelope. The install boot carries the whole release
# in its initramfs and unpacks it into RAM, so it needs more.
SMP := 4
MEM_RUN := 1024
MEM_INSTALL := 4096
OVMF_CODE := /usr/share/OVMF/OVMF_CODE_4M.fd
OVMF_VARS := /usr/share/OVMF/OVMF_VARS_4M.fd

# Each liken lab spells two letters into its MAC bytes and its multicast
# group. liken's dev cluster spells LK and its GitOps lab spells GO;
# this lab spells GC. A stray frame or a duplicate address then names
# the lab it came from, and two labs can run on one machine at once.
UPLINK_MAC := 52:54:00:47:43:01
CLUSTER_MAC := 52:54:00:47:44:01
CLUSTER_SEGMENT := 239.192.71.67:26445

# The flags every boot shares: the UEFI firmware, two network devices,
# and three disks. eth0 is the user-mode network, which reaches the
# host at 10.0.2.2 and forwards the API port. eth1 is the cluster
# segment, a multicast socket that needs no root.
QEMU_COMMON = \
	-accel kvm -accel tcg \
	-cpu max \
	-smp $(SMP) \
	-drive if=pflash,format=raw,readonly=on,file=$(OVMF_CODE) \
	-drive if=pflash,format=raw,file=$(GUEST)/OVMF_VARS.fd \
	-display none \
	-monitor none \
	-netdev user,id=uplink,hostfwd=tcp:127.0.0.1:18443-:6443 \
	-device virtio-net-pci,netdev=uplink,mac=$(UPLINK_MAC) \
	-netdev socket,id=cluster,mcast=$(CLUSTER_SEGMENT) \
	-device virtio-net-pci,netdev=cluster,mac=$(CLUSTER_MAC) \
	-drive file=$(GUEST)/state.qcow2,format=qcow2,if=none,id=state \
	-device virtio-blk-pci,drive=state,serial=liken-$(NODE)-state \
	-drive file=$(GUEST)/pods.qcow2,format=qcow2,if=none,id=pods \
	-device virtio-blk-pci,drive=pods,serial=liken-$(NODE)-pods \
	-drive file=$(GUEST)/boot.qcow2,format=qcow2,if=none,id=boot \
	-device virtio-blk-pci,drive=boot,serial=liken-$(NODE)-boot

.PHONY: fetch identity media install run stop kubeconfig wait forge forge-stop \
	repo deploy smoke clean distclean

# fetch reads the channel, takes that version's own toolkit, and has the
# toolkit download and verify the whole release against release.yaml.
fetch:
	@mkdir -p channel
	@set -eu; \
	version="$(VERSION)"; \
	if [ -z "$$version" ]; then \
		curl -fsS --max-time 60 -o channel/channel.yaml $(SOURCE)/channel.yaml; \
		version="$$(sed -n 's/^latest: *//p' channel/channel.yaml)"; \
	fi; \
	test -n "$$version"; \
	curl -fsS --max-time 300 -o $(BOOTSTRAP) $(SOURCE)/$$version/liken; \
	chmod +x $(BOOTSTRAP); \
	curl -fsS --max-time 60 -o channel/.release.yaml $(SOURCE)/$$version/release.yaml; \
	digest="$$(sha256sum channel/.release.yaml | cut -d' ' -f1)"; \
	$(BOOTSTRAP) fetch -digest sha256:$$digest $(SOURCE) $$version channel; \
	chmod +x channel/$$version/liken; \
	echo "$$version" > channel/version

# identity mints the cluster's certificate authorities and join token
# once. It is private, and git ignores it.
identity: identity/token

identity/token:
	@test -x $(LIKEN)
	$(LIKEN) mint identity

# The layer packs the manifests and the identity. The media puts the
# release under it, as an initramfs QEMU boots directly.
media: install.cpio

deployment.cpio: cluster.yaml machines/node-1.yaml identity/token
	@test -x $(LIKEN)
	$(LIKEN) layer . identity $@

install.cpio: deployment.cpio
	@test -x $(LIKEN)
	$(LIKEN) media $(RELEASE) deployment.cpio $@

# install makes blank disks and a blank UEFI variable store, then boots
# the install media once. The install claims the disks, writes the
# release into slot A, records the boot entries, and powers off.
install: install.cpio
	@test -x $(LIKEN)
	rm -rf $(GUEST)
	mkdir -p $(GUEST)
	qemu-img create -f qcow2 $(GUEST)/state.qcow2 4G
	qemu-img create -f qcow2 $(GUEST)/pods.qcow2 4G
	qemu-img create -f qcow2 $(GUEST)/boot.qcow2 4G
	cp $(OVMF_VARS) $(GUEST)/OVMF_VARS.fd
	timeout $(INSTALL_DEADLINE) qemu-system-x86_64 \
		$(QEMU_COMMON) \
		-m $(MEM_INSTALL) \
		-no-reboot \
		-kernel $(RELEASE)/vmlinuz \
		-initrd install.cpio \
		-append "console=ttyS0 rootfstype=ramfs rdinit=/liken liken.machine=$(NODE) liken.install" \
		-serial file:$(GUEST)/install-console.log \
		</dev/null >$(GUEST)/install-qemu.log 2>&1
	grep -q "liken: installed to slot" $(GUEST)/install-console.log

# The disks and the variable store, for a run target that has no guest
# yet. install makes the same files itself, from nothing.
$(GUEST)/state.qcow2:
	mkdir -p $(GUEST)
	qemu-img create -f qcow2 $@ 4G

$(GUEST)/pods.qcow2:
	mkdir -p $(GUEST)
	qemu-img create -f qcow2 $@ 4G

$(GUEST)/boot.qcow2:
	mkdir -p $(GUEST)
	qemu-img create -f qcow2 $@ 4G

$(GUEST)/OVMF_VARS.fd:
	mkdir -p $(GUEST)
	cp $(OVMF_VARS) $@

# run boots from disk, detached, with QEMU's own pid file as the handle
# stop uses. The console goes to a file a drill can read.
run: $(GUEST)/state.qcow2 $(GUEST)/pods.qcow2 $(GUEST)/boot.qcow2 $(GUEST)/OVMF_VARS.fd
	qemu-system-x86_64 \
		$(QEMU_COMMON) \
		-m $(MEM_RUN) \
		-serial file:$(GUEST)/console.log \
		-pidfile $(PIDFILE) \
		-daemonize

# stop ends the guest by its pid, never by a process name pattern,
# which could match the calling shell. A guest that outlives a drill
# holds the disk images' locks, so this waits for it to exit.
stop:
	@set -eu; \
	test -f $(PIDFILE) || exit 0; \
	pid="$$(cat $(PIDFILE))"; \
	kill "$$pid" 2>/dev/null || true; \
	deadline=$$(( $$(date +%s) + 30 )); \
	while kill -0 "$$pid" 2>/dev/null; do \
		if [ "$$(date +%s)" -ge "$$deadline" ]; then \
			kill -9 "$$pid" 2>/dev/null || true; \
			break; \
		fi; \
		sleep 1; \
	done; \
	rm -f $(PIDFILE)

# kubeconfig mints the admin credential on the host, pointed at the
# forwarded API port. The guest is never asked for it.
kubeconfig:
	@test -x $(LIKEN)
	$(LIKEN) kubeconfig -server https://127.0.0.1:18443 .

# wait polls the node's own Ready condition to a deadline, and prints
# the console's tail when the deadline passes.
wait:
	@set -eu; \
	deadline=$$(( $$(date +%s) + $(READY_DEADLINE) )); \
	while [ "$$(date +%s)" -lt "$$deadline" ]; do \
		if ./kubectl --request-timeout=5s get node $(NODE) --no-headers 2>/dev/null \
			| awk '$$2 == "Ready" { found = 1 } END { exit !found }'; then \
			exit 0; \
		fi; \
		sleep 5; \
	done; \
	echo "lab: $(NODE) not Ready within $(READY_DEADLINE)s" >&2; \
	tail -n 40 $(GUEST)/console.log >&2; \
	exit 1

# forge serves the bare repositories under forge/ with git daemon,
# accepting pushes. The guest reaches it at git://10.0.2.2:9418/, the
# user-mode network's gateway. It needs no credentials.
forge: forge-stop
	mkdir -p forge guests
	git daemon --base-path=forge --export-all --enable=receive-pack --reuseaddr \
		--listen=0.0.0.0 --port=9418 --detach --pid-file=$(FORGE_PIDFILE)

# forge-stop ends the daemon by its pid, so the next forge can bind the
# port.
forge-stop:
	@set -eu; \
	test -f $(FORGE_PIDFILE) || exit 0; \
	pid="$$(cat $(FORGE_PIDFILE))"; \
	kill "$$pid" 2>/dev/null || true; \
	deadline=$$(( $$(date +%s) + 30 )); \
	while kill -0 "$$pid" 2>/dev/null; do \
		if [ "$$(date +%s)" -ge "$$deadline" ]; then \
			kill -9 "$$pid" 2>/dev/null || true; \
			break; \
		fi; \
		sleep 1; \
	done; \
	rm -f $(FORGE_PIDFILE)

# repo makes forge/NAME.git, a bare repository seeded from
# fixtures/NAME with one commit on main.
repo:
	@test -n "$(NAME)"
	@test -d fixtures/$(NAME)
	@set -eu; \
	rm -rf forge/$(NAME).git guests/seed-$(NAME); \
	mkdir -p forge guests; \
	git init -q --bare -b main forge/$(NAME).git; \
	git init -q -b main guests/seed-$(NAME); \
	cp -R fixtures/$(NAME)/. guests/seed-$(NAME)/; \
	git -C guests/seed-$(NAME) add -A; \
	git -C guests/seed-$(NAME) \
		-c user.name=lab -c user.email=lab@liken.sh \
		commit -q -m "$(NAME)"; \
	git -C guests/seed-$(NAME) push -q "$(CURDIR)/forge/$(NAME).git" main

# deploy applies this repository's deploy/ with the image pinned to a
# build CI pushed. The node reports Ready before k3s has made the
# liken-system namespace, so the apply retries to a deadline. It then
# waits for every DaemonSet the base carries.
deploy:
	@set -eu; \
	tag="$(IMAGE_TAG)"; \
	test -n "$$tag"; \
	mkdir -p guests/deploy; \
	rm -f guests/deploy/doc-*.yaml; \
	printf '%s\n' \
		'apiVersion: kustomize.config.k8s.io/v1beta1' \
		'kind: Kustomization' \
		'resources:' \
		'  - ../../../deploy' \
		'images:' \
		'  - name: $(IMAGE)' \
		"    newTag: $$tag" \
		> guests/deploy/kustomization.yaml; \
	echo "lab: $(IMAGE):$$tag"; \
	kustomize build guests/deploy > guests/deploy/rendered.yaml; \
	deadline=$$(( $$(date +%s) + $(DEPLOY_DEADLINE) )); \
	until ./kubectl apply -f guests/deploy/rendered.yaml; do \
		test "$$(date +%s)" -lt "$$deadline" || { \
			echo "lab: deploy/ did not apply within $(DEPLOY_DEADLINE)s" >&2; \
			exit 1; \
		}; \
		sleep 5; \
	done; \
	awk '/^---$$/ { n++; next } { print > sprintf("guests/deploy/doc-%02d.yaml", n) }' \
		n=0 guests/deploy/rendered.yaml; \
	for doc in guests/deploy/doc-*.yaml; do \
		grep -q '^kind: DaemonSet' "$$doc" || continue; \
		./kubectl rollout status -f "$$doc" --timeout=$(DEPLOY_DEADLINE)s; \
	done

# smoke runs the whole chain from nothing to a node that lists the
# driver, and ends by reading where the store lands on the node's disks.
smoke:
	$(MAKE) clean
	$(MAKE) fetch
	$(MAKE) identity
	$(MAKE) media
	$(MAKE) install
	$(MAKE) run
	$(MAKE) kubeconfig
	$(MAKE) wait
	$(MAKE) forge
	$(MAKE) repo NAME=hello
	$(MAKE) deploy
	@# The kubelet registers the driver after the pod is ready, so the
	@# csinode entry follows the rollout by a few seconds.
	@set -eu; \
	deadline=$$(( $$(date +%s) + $(CSINODE_DEADLINE) )); \
	until ./kubectl get csinode $(NODE) \
		-o jsonpath='{.spec.drivers[*].name}' 2>/dev/null | grep -q git.liken.sh; do \
		test "$$(date +%s)" -lt "$$deadline" || { \
			echo "lab: git.liken.sh is not on csinode/$(NODE) within $(CSINODE_DEADLINE)s" >&2; \
			exit 1; \
		}; \
		sleep 5; \
	done
	./kubectl get csinode $(NODE) -o wide
	@# A privileged pod reads the node's own mount table for the store
	@# path, so a change to liken's disk layout shows up here first.
	@# Busybox has no findmnt, hence Debian.
	printf '%s\n' \
		'apiVersion: v1' \
		'kind: Pod' \
		'metadata:' \
		'  name: store-check' \
		'  namespace: default' \
		'spec:' \
		'  hostNetwork: true' \
		'  hostPID: true' \
		'  nodeSelector: {liken.sh/machine: "true"}' \
		'  tolerations: [{operator: Exists}]' \
		'  restartPolicy: Never' \
		'  containers:' \
		'    - name: store-check' \
		'      image: debian:12-slim' \
		'      command: ["sh", "-c", "sleep infinity"]' \
		'      securityContext: {privileged: true}' \
		'      volumeMounts:' \
		'        - name: host' \
		'          mountPath: /host' \
		'          mountPropagation: HostToContainer' \
		'  volumes:' \
		'    - {name: host, hostPath: {path: /}}' \
		> guests/store-check.yaml
	./kubectl delete pod store-check --ignore-not-found --timeout=60s
	./kubectl apply -f guests/store-check.yaml
	./kubectl wait --for=condition=Ready pod/store-check --timeout=$(STORE_DEADLINE)s
	./kubectl exec store-check -- sh -c \
		'for p in /host/var/lib/liken/pod-storage/git-csi /host/var/lib/liken/pod-storage; do \
			findmnt --target "$$p" -o TARGET,SOURCE,FSTYPE || continue; \
			echo "store path: $${p#/host}"; \
			break; \
		done'

# clean is a factory reset of the guest. It keeps the download and the
# identity.
clean: stop forge-stop
	rm -rf guests

# distclean removes everything git does not carry.
distclean: clean
	rm -rf channel identity deployment.cpio install.cpio
