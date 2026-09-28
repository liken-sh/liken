#!/usr/bin/env bash
# Drill 06. Divergence and restore against the host forge: a
# conflicting push on the forge sends the volume to its side branch, a
# merge on the forge heals it at the next stage, and a volume made again
# on a guest installed from nothing returns the tree with its modes and
# its empty directory.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

# The forge is on the host, and the guest reaches it at the
# user-mode network's gateway.
GUEST_FORGE="git://10.0.2.2:9418/hello.git"
FORGE="$LAB/forge/hello.git"

# The writer image is one the lab already pulled, so no drill
# waits on a registry.
IMAGE="${IMAGE:-debian:12-slim}"
DRIVER_NAMESPACE="${DRIVER_NAMESPACE:-liken-system}"
NAMESPACE=drill-06
# The store on the node keeps a work tree per handle, and the forge is
# reseeded on every run, so a handle carries a run's own stamp. A handle
# reused across runs would find the last run's tree and push against a
# history the forge no longer holds.
RUN="$(date +%s)"
HANDLE="drill-06-$RUN"
CLASS=drill-06
# The side branch the driver takes is the ref and the volume's
# handle, which is what plan 06 names.
SIDE="main.$HANDLE"

# Every wait in this drill has a deadline, in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
ARMED_DEADLINE="${ARMED_DEADLINE:-180}"
PUSH_DEADLINE="${PUSH_DEADLINE:-180}"
EVENT_DEADLINE="${EVENT_DEADLINE:-180}"
HEAL_DEADLINE="${HEAL_DEADLINE:-240}"
INSTALL_DEADLINE="${INSTALL_DEADLINE:-900}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

# Cleanup leaves the cluster as the drill found it, however the
# drill ends.
cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$HANDLE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete volumeattributesclass "$CLASS" --ignore-not-found >/dev/null 2>&1 || true
	rm -rf "$LAB/guests/drill-06-clone"
	return "$status"
}
trap cleanup EXIT

# Driver_log is what the node plugin wrote about this volume.
driver_log() {
	kube logs -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node -c driver --tail=400 2>/dev/null || true
}

# The driver's own metrics, read through the API server's pod
# proxy, because the DaemonSet publishes the port and no Service.
driver_metrics() {
	local pod
	pod="$(kube get pods -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
	test -n "$pod" || return 0
	kube get --raw "/api/v1/namespaces/$DRIVER_NAMESPACE/pods/$pod:9808/proxy/metrics" 2>/dev/null || true
}

# Wait_for_metric waits until the driver's own gauges carry the
# pattern, or fails on the deadline.
wait_for_metric() {
	local pattern="$1" deadline="$2" what="$3"
	local limit
	limit=$(($(date +%s) + deadline))
	while [ "$(date +%s)" -lt "$limit" ]; do
		if driver_metrics | grep -qE "$pattern"; then
			echo "drill 06: the metrics report $what"
			return 0
		fi
		sleep 3
	done
	echo "drill 06: the metrics did not report $what within ${deadline}s" >&2
	driver_metrics | grep git_csi >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

# Wait_for_event waits until the namespace's events carry the
# pattern, or fails on the deadline.
wait_for_event() {
	local pattern="$1" deadline
	deadline=$(($(date +%s) + EVENT_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if kube get events -n "$NAMESPACE" -o yaml 2>/dev/null | grep -qE "$pattern"; then
			echo "drill 06: the events report $pattern"
			return 0
		fi
		sleep 3
	done
	echo "drill 06: no event matched $pattern within ${EVENT_DEADLINE}s" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	return 1
}

# Wait_for_content waits until the branch on the host forge holds the
# text in the file, which is the whole proof that a push landed.
wait_for_content() {
	local branch="$1" path="$2" want="$3" deadline="$4"
	local limit
	limit=$(($(date +%s) + deadline))
	while [ "$(date +%s)" -lt "$limit" ]; do
		if git -C "$FORGE" show "$branch:$path" 2>/dev/null | grep -qxF "$want"; then
			echo "drill 06: $branch holds $want in $path"
			return 0
		fi
		sleep 3
	done
	echo "drill 06: $branch did not hold $want in $path within ${deadline}s" >&2
	git -C "$FORGE" log --oneline -5 --all >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

# Wait_for_branch waits until the host forge holds the branch, or holds
# it no longer when the second argument is "gone".
wait_for_branch() {
	local branch="$1" state="$2" deadline="$3"
	local limit found
	limit=$(($(date +%s) + deadline))
	while [ "$(date +%s)" -lt "$limit" ]; do
		found=no
		if git -C "$FORGE" for-each-ref --format='%(refname:short)' refs/heads/ |
			grep -qxF "$branch"; then
			found=yes
		fi
		if { [ "$state" = "gone" ] && [ "$found" = "no" ]; } ||
			{ [ "$state" = "there" ] && [ "$found" = "yes" ]; }; then
			echo "drill 06: the forge holds $branch: $found"
			return 0
		fi
		sleep 3
	done
	echo "drill 06: $branch was not $state within ${deadline}s" >&2
	git -C "$FORGE" for-each-ref --format='%(refname:short)' refs/heads/ >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

# Forge_clone is a working copy of the host forge, which is where a
# person on the forge commits and merges.
forge_clone() {
	rm -rf "$LAB/guests/drill-06-clone"
	git clone --quiet "$FORGE" "$LAB/guests/drill-06-clone"
}

# Class writes the policy the claim names. The quiesce is short, so the
# drill waits seconds for a commit and not minutes.
class() {
	kube apply -f - >/dev/null <<YAML
apiVersion: storage.k8s.io/v1
kind: VolumeAttributesClass
metadata:
  name: $CLASS
driverName: git.liken.sh
parameters:
  push.quiesce: 10s
  push.maxLatency: 5m
  commit.maxFileSize: 1Mi
  commit.author: The lab <lab@liken.sh>
YAML
}

# Volume writes a PersistentVolume against the host forge, and a claim on
# it that names the class.
volume() {
	kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: $HANDLE
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOncePod]
  persistentVolumeReclaimPolicy: Retain
  # VolumeAttributesClassName must match the claim's, or the binder refuses the pair
  volumeAttributesClassName: $CLASS
  csi:
    driver: git.liken.sh
    volumeHandle: $HANDLE
    volumeAttributes:
      url: $GUEST_FORGE
      ref: main
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: config
  namespace: $NAMESPACE
spec:
  volumeName: $HANDLE
  volumeAttributesClassName: $CLASS
  accessModes: [ReadWriteOncePod]
  resources: {requests: {storage: 1Gi}}
  storageClassName: ""
YAML
}

# Writer holds the volume open, so a drill can write into the tree with
# kubectl exec.
writer() {
	kube apply -n "$NAMESPACE" -f - >/dev/null <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: writer
spec:
  restartPolicy: Never
  tolerations:
    - operator: Exists
  containers:
    - name: writer
      image: $IMAGE
      command: ["sh", "-c", "sleep infinity"]
      volumeMounts:
        - name: config
          mountPath: /config
  volumes:
    - name: config
      persistentVolumeClaim:
        claimName: config
YAML
	kube wait -n "$NAMESPACE" --for=condition=Ready pod/writer --timeout="${READY_DEADLINE}s"
}

echo "drill 06: the node"
kube get nodes

echo "drill 06: the forge, with hello on it"
make -C "$LAB" forge
make -C "$LAB" repo NAME=hello

echo "drill 06: the class, the volume, and its claim"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
class
volume

echo "drill 06: a pod that writes"
writer
kube exec -n "$NAMESPACE" writer -- sh -c \
	'cd /config && echo pod-one > one.yaml && echo secret > secret.yaml &&
	chmod 600 secret.yaml && mkdir -p storage'
wait_for_metric "git_csi_armed\{claim=\"config\",namespace=\"$NAMESPACE\"\} 1" \
	"$ARMED_DEADLINE" "the volume armed"
wait_for_content main one.yaml pod-one "$PUSH_DEADLINE"

echo "drill 06: a person pushes a conflicting change to the same file"
forge_clone
(
	cd "$LAB/guests/drill-06-clone"
	echo forge-one >one.yaml
	git -c user.name=lab -c user.email=lab@liken.sh commit --quiet -am "the forge writes one.yaml"
	git push --quiet origin main
)
git -C "$FORGE" log --oneline -1 main

echo "drill 06: the pod writes again and the driver's push is rejected"
kube exec -n "$NAMESPACE" writer -- sh -c 'cd /config && echo pod-two > one.yaml'
kube delete -n "$NAMESPACE" pod/writer --timeout="${READY_DEADLINE}s"

echo "drill 06: the volume takes its side branch"
wait_for_branch "$SIDE" there "$PUSH_DEADLINE"
wait_for_content "$SIDE" one.yaml pod-two "$PUSH_DEADLINE"
if git -C "$FORGE" show main:one.yaml | grep -qxF pod-two; then
	echo "drill 06: main holds the pod's work, and the rejection must have kept it off" >&2
	exit 1
fi
echo "drill 06: main still holds the forge's change"
wait_for_event "GitVolumeDiverged" || true

echo "drill 06: the volume is diverged in all three places"
writer
wait_for_metric "git_csi_diverged\{claim=\"config\",namespace=\"$NAMESPACE\"\} 1" \
	"$PUSH_DEADLINE" "the volume diverged"
driver_log | grep -E "diverged" | tail -n 5

echo "drill 06: a person merges the side branch on the forge"
forge_clone
(
	cd "$LAB/guests/drill-06-clone"
	git -c user.name=lab -c user.email=lab@liken.sh \
		merge --quiet --no-ff -m "merge $SIDE" "origin/$SIDE" || {
		git checkout --theirs one.yaml
		git add one.yaml
		git -c user.name=lab -c user.email=lab@liken.sh commit --quiet -m "merge $SIDE"
	}
	git push --quiet origin main
)
git -C "$FORGE" log --oneline -3 main

echo "drill 06: the next stage heals the volume"
kube delete -n "$NAMESPACE" pod/writer --timeout="${READY_DEADLINE}s"
writer
wait_for_branch "$SIDE" gone "$HEAL_DEADLINE"
wait_for_metric "git_csi_diverged\{claim=\"config\",namespace=\"$NAMESPACE\"\} 0" \
	"$HEAL_DEADLINE" "the volume healed"
wait_for_event "GitVolumeHealed" || true
found="$(kube exec -n "$NAMESPACE" writer -- cat /config/one.yaml | tr -d '[:space:]')"
if [ "$found" != "pod-two" ]; then
	echo "drill 06: the healed tree holds $found in one.yaml, want pod-two" >&2
	exit 1
fi
echo "drill 06: the healed tree is on main and holds the merged work"

echo "drill 06: the pod writes on main again"
kube exec -n "$NAMESPACE" writer -- sh -c 'cd /config && echo pod-three > three.yaml'
wait_for_content main three.yaml pod-three "$PUSH_DEADLINE"

echo "drill 06: the metadata ref is on the forge"
git -C "$FORGE" show refs/git-csi/metadata:metadata

echo "drill 06: a guest installed from nothing"
kube delete -n "$NAMESPACE" pod/writer --timeout="${READY_DEADLINE}s"
make -C "$LAB" stop
# Clean removes guests/ alone, so the forge and its history stay
# on the host and the restore has something to restore from.
make -C "$LAB" clean
timeout "$INSTALL_DEADLINE" make -C "$LAB" install
make -C "$LAB" run
make -C "$LAB" kubeconfig
make -C "$LAB" wait
make -C "$LAB" forge
make -C "$LAB" deploy
kube get nodes

echo "drill 06: the same volume on the new guest"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
class
volume
writer
kube exec -n "$NAMESPACE" writer -- ls -la /config

echo "drill 06: the restored tree holds what the last push carried"
found="$(kube exec -n "$NAMESPACE" writer -- cat /config/three.yaml | tr -d '[:space:]')"
if [ "$found" != "pod-three" ]; then
	echo "drill 06: the restored tree holds $found in three.yaml, want pod-three" >&2
	exit 1
fi
found="$(kube exec -n "$NAMESPACE" writer -- stat -c '%a' /config/secret.yaml | tr -d '[:space:]')"
if [ "$found" != "600" ]; then
	echo "drill 06: secret.yaml is $found on the restored volume, want 600" >&2
	exit 1
fi
if ! kube exec -n "$NAMESPACE" writer -- test -d /config/storage; then
	echo "drill 06: the empty directory did not return to the restored volume" >&2
	exit 1
fi
echo "drill 06: the restored tree holds secret.yaml at 600 and the empty directory"

echo "drill 06: passed"
