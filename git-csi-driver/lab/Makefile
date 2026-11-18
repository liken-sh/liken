# The lab is one liken machine in QEMU, booted from the public release
# channel, and a git forge on the host that the machine reaches over the
# user-mode network. Every drill of the driver runs here, so that a
# person sees a change work before it is installed on a real cluster.
# The targets, disk names, firmware paths, and network devices follow
# liken/dev-cluster/Makefile, for one node.

# A recipe that fails or is killed can leave its target partly written
# and newer than its inputs, so make deletes the target on failure.
.DELETE_ON_ERROR:

NODE := node-1
GUEST := guests/$(NODE)
PIDFILE := $(GUEST)/qemu.pid
FORGE_PIDFILE := guests/forge.pid

# Where releases come from, and the version this checkout runs. fetch
# writes the version it resolved to channel/version, so later targets
# use the same release without querying the channel again.
SOURCE ?= https://releases.liken.sh
VERSION ?= $(shell cat channel/version 2>/dev/null)
RELEASE := channel/$(VERSION)
LIKEN := $(RELEASE)/liken

# fetch downloads the release's own liken binary here and runs it to
# fetch the release. The format of the install media changes with the
# liken binary, so each target runs the binary of the release it builds.
BOOTSTRAP := channel/.liken

# The image that the deploy target pins. The default tag is the newest
# tag that CI pushed to the package registry, so a drill after a push
# to main runs that build.
IMAGE ?= ghcr.io/liken-sh/git-csi-driver
IMAGE_TAG ?= $(shell gh api /orgs/liken-sh/packages/container/git-csi-driver/versions \
	--jq '[.[].metadata.container.tags[]] | map(select(test("^[0-9]"))) | .[0]' 2>/dev/null)

# The deadlines of the waits in this file, in seconds.
INSTALL_DEADLINE ?= 600
READY_DEADLINE ?= 300
DEPLOY_DEADLINE ?= 300
CSINODE_DEADLINE ?= 300
STORE_DEADLINE ?= 300

# The guest's size, as in liken's dev cluster. A boot from disk gets
# 1 GiB, the memory that liken's plans budget for a machine. The install
# boot unpacks the whole release from its initramfs into RAM, so it
# gets more.
SMP := 4
MEM_RUN := 1024
MEM_INSTALL := 4096
OVMF_CODE := /usr/share/OVMF/OVMF_CODE_4M.fd
OVMF_VARS := /usr/share/OVMF/OVMF_VARS_4M.fd

# Each liken lab puts two ASCII letters in its MAC addresses and its
# multicast group: LK in liken's dev cluster, GO in its GitOps lab, and
# GC here, as 47:43 in the MACs and 71.67 in the group. So a stray frame
# shows which lab sent it, and two labs can run on one host at once.
UPLINK_MAC := 52:54:00:47:43:01
CLUSTER_MAC := 52:54:00:47:44:01
CLUSTER_SEGMENT := 239.192.71.67:26445

# The flags that every boot shares: the UEFI firmware, two network
# devices, and three disks. eth0 is the user-mode network. The guest
# reaches the host at 10.0.2.2, and QEMU forwards the API port. eth1 is
# the cluster segment, a multicast socket that needs no root.
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

# fetch reads the channel, downloads that version's liken binary, and
# runs it to download the whole release and verify it against
# release.yaml.
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

# identity creates the cluster's certificate authorities and join token
# once. It is secret, and git ignores it.
identity: identity/token

identity/token:
	@test -x $(LIKEN)
	$(LIKEN) mint identity

# The layer packs the manifests and the identity. The media target adds
# the release to it, as an initramfs that QEMU boots directly.
media: install.cpio

deployment.cpio: cluster.yaml machines/node-1.yaml identity/token
	@test -x $(LIKEN)
	$(LIKEN) layer . identity $@

install.cpio: deployment.cpio
	@test -x $(LIKEN)
	$(LIKEN) media $(RELEASE) deployment.cpio $@

# install creates blank disks and a blank UEFI variable store, then
# boots the install media once. The installer claims the disks,
# writes the release into slot A, records the boot entries, and powers
# off.
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

# The disks and variable store for run when no guest exists yet.
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

# run boots from disk in the background. QEMU writes the pid file that
# stop reads, and the console goes to a file that a drill can read.
run: $(GUEST)/state.qcow2 $(GUEST)/pods.qcow2 $(GUEST)/boot.qcow2 $(GUEST)/OVMF_VARS.fd
	qemu-system-x86_64 \
		$(QEMU_COMMON) \
		-m $(MEM_RUN) \
		-serial file:$(GUEST)/console.log \
		-pidfile $(PIDFILE) \
		-daemonize

# stop kills the guest by pid, not by a process name pattern, which
# could match the calling shell. A running guest locks the disk images,
# so this waits for it to exit.
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

# kubeconfig creates the admin credential on the host from the identity,
# for the forwarded API port. It reads nothing from the guest.
kubeconfig:
	@test -x $(LIKEN)
	$(LIKEN) kubeconfig -server https://127.0.0.1:18443 .

# wait polls the node's Ready condition until a deadline, and prints
# the end of the console log if the deadline passes.
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

# forge serves the bare repositories under forge/ with git daemon, and
# accepts pushes without credentials. The guest reaches it at
# git://10.0.2.2:9418/, the user-mode network's gateway.
forge: forge-stop
	mkdir -p forge guests
	git daemon --base-path=forge --export-all --enable=receive-pack --reuseaddr \
		--listen=0.0.0.0 --port=9418 --detach --pid-file=$(FORGE_PIDFILE)

# forge-stop kills the daemon by pid, so that the next forge can bind
# the port.
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

# repo creates the bare repository forge/NAME.git, with one commit on
# main that contains fixtures/NAME.
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
# build that CI pushed. The node is Ready before k3s creates the
# liken-system namespace, so the apply retries until a deadline. Then it
# waits for the rollout of every DaemonSet in the manifests.
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

# smoke runs every step from an empty lab to a node that lists the
# driver, then reads which of the node's disks holds the store.
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
	@# CSINode entry appears a few seconds after the rollout.
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
	@# A privileged pod reads the node's mount table for the store path,
	@# so a change to liken's disk layout shows up here first. The image
	@# is Debian because Busybox has no findmnt.
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
		'      image: debian:sid-slim' \
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

# clean resets the guest to a blank state. It keeps the downloaded
# release and the identity.
clean: stop forge-stop
	rm -rf guests

# distclean removes every file that git does not track.
distclean: clean
	rm -rf channel identity deployment.cpio install.cpio
