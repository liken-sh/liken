# The lab boots one liken machine in QEMU from the public release
# channel, and runs a container registry on the host that serves the
# image built from this checkout. Every drill of this driver runs here
# before the driver goes on a real cluster. The targets, disk names,
# firmware paths, and network devices follow liken's
# dev-cluster/Makefile for a single node.

# A recipe that fails, or a build that is killed during one, leaves its
# target partly written and newer than its inputs. make deletes that
# target, so the next build makes it again and does not take it as
# current.
.DELETE_ON_ERROR:

NODE := node-1
GUEST := guests/$(NODE)
PIDFILE := $(GUEST)/qemu.pid

# fetch writes the version it resolved to channel/version, so every
# later target uses the same release without reading the channel again.
SOURCE ?= https://releases.liken.sh
VERSION ?= $(shell cat channel/version 2>/dev/null)
RELEASE := channel/$(VERSION)
LIKEN := $(RELEASE)/liken

# fetch downloads the liken binary of the release it fetches, and uses
# it to fetch the rest. The media format changes with the liken binary,
# so the media of a release must be built by that release's own binary.
BOOTSTRAP := channel/.liken

# REGISTRY_HOST is the registry name the guest pulls from, and
# cluster.yaml mirrors it to the host. REGISTRY_PUSH is the address the
# host pushes to. docker allows plain HTTP to a loopback address with
# no daemon configuration, so the push needs no TLS.
REGISTRY_HOST ?= lab.registry:5000
REGISTRY_PUSH ?= 127.0.0.1:5000
REGISTRY_NAME ?= per-node-lab-registry
REGISTRY_IMAGE ?= registry:3

# The image that deploy runs. The default is the image that the image
# target builds from this checkout. Set IMAGE and IMAGE_TAG to run an
# image that CI published instead.
IMAGE ?= $(REGISTRY_HOST)/per-node-csi-driver
IMAGE_TAG ?= dev

# Deadlines, in seconds.
INSTALL_DEADLINE ?= 600
READY_DEADLINE ?= 300
DEPLOY_DEADLINE ?= 300
CSINODE_DEADLINE ?= 300
STORE_DEADLINE ?= 300

# The CPU and memory follow liken's dev cluster. A boot from disk runs
# in 1 GiB. The install boot unpacks the whole release from its
# initramfs into RAM, so it needs more.
SMP := 4
MEM_RUN := 1024
MEM_INSTALL := 4096
OVMF_CODE := /usr/share/OVMF/OVMF_CODE_4M.fd
OVMF_VARS := /usr/share/OVMF/OVMF_VARS_4M.fd

# Each liken lab puts its own two ASCII letters into its MAC addresses
# and its multicast group: LK for liken's dev cluster, GO for its
# GitOps lab, GC for the git CSI driver's lab, and PN for this lab. So
# two labs can run on one host without an address conflict, and a stray
# frame shows which lab sent it.
UPLINK_MAC := 52:54:00:50:4e:01
CLUSTER_MAC := 52:54:00:50:4f:01
CLUSTER_SEGMENT := 239.192.80.78:26445

# eth0 is QEMU's user-mode network. The guest reaches the host at
# 10.0.2.2, and the host reaches the guest's API server at
# 127.0.0.1:18444. eth1 is the cluster segment, a multicast socket,
# which needs no root on the host.
QEMU_COMMON = \
	-accel kvm -accel tcg \
	-cpu max \
	-smp $(SMP) \
	-drive if=pflash,format=raw,readonly=on,file=$(OVMF_CODE) \
	-drive if=pflash,format=raw,file=$(GUEST)/OVMF_VARS.fd \
	-display none \
	-monitor none \
	-netdev user,id=uplink,hostfwd=tcp:127.0.0.1:18444-:6443 \
	-device virtio-net-pci,netdev=uplink,mac=$(UPLINK_MAC) \
	-netdev socket,id=cluster,mcast=$(CLUSTER_SEGMENT) \
	-device virtio-net-pci,netdev=cluster,mac=$(CLUSTER_MAC) \
	-drive file=$(GUEST)/state.qcow2,format=qcow2,if=none,id=state \
	-device virtio-blk-pci,drive=state,serial=liken-$(NODE)-state \
	-drive file=$(GUEST)/pods.qcow2,format=qcow2,if=none,id=pods \
	-device virtio-blk-pci,drive=pods,serial=liken-$(NODE)-pods \
	-drive file=$(GUEST)/boot.qcow2,format=qcow2,if=none,id=boot \
	-device virtio-blk-pci,drive=boot,serial=liken-$(NODE)-boot

.PHONY: fetch identity media install run stop kubeconfig wait \
	registry registry-stop image deploy smoke drill clean distclean

# fetch reads the latest version from the channel when VERSION is not
# set. It downloads that version's liken binary, which downloads the
# release and verifies it against the digest of release.yaml.
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

# identity mints the cluster's certificate authorities and join token,
# once. They are secret, and git ignores them.
identity: identity/token

identity/token:
	@test -x $(LIKEN)
	$(LIKEN) mint identity

# deployment.cpio packs the manifests and the identity. install.cpio
# adds the release to it, as an initramfs that QEMU boots directly.
media: install.cpio

deployment.cpio: cluster.yaml machines/node-1.yaml identity/token
	@test -x $(LIKEN)
	$(LIKEN) layer . identity $@

install.cpio: deployment.cpio
	@test -x $(LIKEN)
	$(LIKEN) media $(RELEASE) deployment.cpio $@

# install makes blank disks and a blank UEFI variable store, then boots
# the install media. The installer claims the disks, writes the
# release into slot A, writes the boot entries, and powers off.
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

# These rules let run start a guest that was never installed.
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

# run boots from disk in the background. stop reads QEMU's pid file,
# and a drill can read the console in console.log.
run: $(GUEST)/state.qcow2 $(GUEST)/pods.qcow2 $(GUEST)/boot.qcow2 $(GUEST)/OVMF_VARS.fd
	qemu-system-x86_64 \
		$(QEMU_COMMON) \
		-m $(MEM_RUN) \
		-serial file:$(GUEST)/console.log \
		-pidfile $(PIDFILE) \
		-daemonize

# stop kills the guest by its pid, not by a process name pattern, which
# can match the calling shell. A running guest keeps the locks on the
# disk images, so stop waits up to 30 seconds for it to exit and then
# kills it with SIGKILL.
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

# kubeconfig mints an admin credential on the host from the identity,
# for the forwarded API port. It does not connect to the guest.
kubeconfig:
	@test -x $(LIKEN)
	$(LIKEN) kubeconfig -server https://127.0.0.1:18444 .

# wait polls until the node is Ready. When READY_DEADLINE passes, it
# prints the last 40 lines of the console and fails.
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

# registry starts the registry unless it is already running. The guest
# pulls from it at http://10.0.2.2:5000 through the mirror in
# cluster.yaml, with no credentials.
registry:
	@set -eu; \
	if [ -z "$$(docker ps -q --filter name=^$(REGISTRY_NAME)$$)" ]; then \
		docker rm -f $(REGISTRY_NAME) >/dev/null 2>&1 || true; \
		docker run -d --name $(REGISTRY_NAME) \
			-p $(REGISTRY_PUSH):5000 $(REGISTRY_IMAGE) >/dev/null; \
	fi; \
	docker ps --filter name=^$(REGISTRY_NAME)$$ --format 'lab: registry {{.Status}}'

# registry-stop deletes the registry container.
registry-stop:
	@docker rm -f $(REGISTRY_NAME) >/dev/null 2>&1 || true

# image builds this checkout and pushes it to the host's registry with
# the tag that deploy uses. It builds through the repository's bake
# file, because the bake file supplies the kubernetes and notices
# directories as the build contexts that the Dockerfile copies from.
image: registry
	cd ../.. && docker buildx bake per-node-csi-driver --load \
		--set 'per-node-csi-driver.tags=$(REGISTRY_PUSH)/per-node-csi-driver:$(IMAGE_TAG)'
	docker push $(REGISTRY_PUSH)/per-node-csi-driver:$(IMAGE_TAG)

# deploy applies deploy/ with the lab's image and with the sweep
# interval of sweep-every.yaml. The node can be Ready before k3s has
# created the liken-system namespace, so the apply retries until
# DEPLOY_DEADLINE. Then deploy waits for the rollout of each DaemonSet.
deploy:
	@set -eu; \
	tag="$(IMAGE_TAG)"; \
	test -n "$$tag"; \
	mkdir -p guests/deploy; \
	rm -f guests/deploy/doc-*.yaml; \
	cp sweep-every.yaml guests/deploy/sweep-every.yaml; \
	printf '%s\n' \
		'apiVersion: kustomize.config.k8s.io/v1beta1' \
		'kind: Kustomization' \
		'resources:' \
		'  - ../../../deploy' \
		'patches:' \
		'  - path: sweep-every.yaml' \
		'images:' \
		'  - name: ghcr.io/liken-sh/per-node-csi-driver' \
		'    newName: $(IMAGE)' \
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

# smoke builds the lab from nothing, deploys the driver, waits until the
# node's CSINode lists it, and prints the filesystem that holds the
# store on the node.
smoke:
	$(MAKE) clean
	$(MAKE) fetch
	$(MAKE) identity
	$(MAKE) media
	$(MAKE) install
	$(MAKE) run
	$(MAKE) kubeconfig
	$(MAKE) wait
	$(MAKE) image
	$(MAKE) deploy
	@# The kubelet registers the driver after the pod is Ready, so the
	@# CSINode can lack the driver when the rollout ends.
	@set -eu; \
	deadline=$$(( $$(date +%s) + $(CSINODE_DEADLINE) )); \
	until ./kubectl get csinode $(NODE) \
		-o jsonpath='{.spec.drivers[*].name}' 2>/dev/null | grep -q per-node.liken.sh; do \
		test "$$(date +%s)" -lt "$$deadline" || { \
			echo "lab: per-node.liken.sh is not on csinode/$(NODE) within $(CSINODE_DEADLINE)s" >&2; \
			exit 1; \
		}; \
		sleep 5; \
	done
	./kubectl get csinode $(NODE) -o wide
	@# A privileged pod runs findmnt on the store path in the node's
	@# mount table, so a change to liken's disk layout shows in this
	@# output. The image is Debian because busybox has no findmnt.
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
		'for p in /host/var/lib/liken/pod-storage/per-node /host/var/lib/liken/pod-storage; do \
			findmnt --target "$$p" -o TARGET,SOURCE,FSTYPE || continue; \
			echo "store path: $${p#/host}"; \
			break; \
		done'

# drill runs each drill in order and stops at the first failure.
drill:
	@set -eu; \
	for one in drills/*.sh; do \
		echo "lab: $$one"; \
		bash "$$one"; \
	done

# clean stops the guest and deletes its disks. It keeps the downloaded
# release and the identity.
clean: stop
	rm -rf guests

# distclean also deletes the registry, the downloaded release, the
# identity, and the media.
distclean: clean registry-stop
	rm -rf channel identity deployment.cpio install.cpio
