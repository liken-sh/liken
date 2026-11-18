#!/usr/bin/env bash
# Drill 05. A writeable volume against the host forge. A
# VolumeAttributesClass arms the volume, and the driver commits and
# pushes what the pod writes. A claim moved to another class takes the
# new policy without a plugin restart. The ignore list and
# commit.maxFileSize keep files out of the commit. A pod deleted before
# the push timer fires still pushes, at unpublish. A new volume handle on
# the same repository restores the file modes and the empty directory.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

# The forge is a git daemon on the host. 10.0.2.2 is the host as a QEMU
# guest sees it on user-mode networking.
GUEST_FORGE="git://10.0.2.2:9418/hello.git"
FORGE="$LAB/forge/hello.git"

# The writer image is already in the lab's image store, so the drill
# does not wait on a registry.
IMAGE="${IMAGE:-debian:sid-slim}"
DRIVER_NAMESPACE="${DRIVER_NAMESPACE:-liken-system}"
NAMESPACE=drill-05
# The node's store keeps a work tree for each volume handle, and
# `make repo` creates the forge's repository again on every run. A
# handle from an earlier run would find that run's tree and push commits
# the new repository does not have, so each handle includes the run's
# start time.
RUN="$(date +%s)"
HANDLE="drill-05-$RUN"
# The restore volume uses a handle the node has never seen, on the same
# repository.
RESTORE_HANDLE="drill-05-restore-$RUN"
CLASS=drill-05
SECOND_CLASS=drill-05-eager

# Every wait in this drill gives up after a deadline in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
ARMED_DEADLINE="${ARMED_DEADLINE:-180}"
PUSH_DEADLINE="${PUSH_DEADLINE:-180}"
EVENT_DEADLINE="${EVENT_DEADLINE:-180}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$HANDLE" "$RESTORE_HANDLE" \
		--ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete volumeattributesclass "$CLASS" "$SECOND_CLASS" --ignore-not-found >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT

driver_log() {
	kube logs -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node -c driver --tail=400 2>/dev/null || true
}

# driver_pod prints the node plugin pod's name and restart count, so the
# drill can tell whether the plugin restarted.
driver_pod() {
	kube get pods -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node \
		-o jsonpath='{.items[0].metadata.name}:{.items[0].status.containerStatuses[0].restartCount}' \
		2>/dev/null || true
}

# driver_metrics reads the driver's metrics through the API server's pod
# proxy, because the DaemonSet has no Service for the metrics port.
driver_metrics() {
	local pod
	pod="$(kube get pods -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
	test -n "$pod" || return 0
	kube get --raw "/api/v1/namespaces/$DRIVER_NAMESPACE/pods/$pod:9808/proxy/metrics" 2>/dev/null || true
}

wait_for_metric() {
	local pattern="$1" deadline="$2" what="$3"
	local limit
	limit=$(($(date +%s) + deadline))
	while [ "$(date +%s)" -lt "$limit" ]; do
		if driver_metrics | grep -qE "$pattern"; then
			echo "drill 05: the metrics report $what"
			return 0
		fi
		sleep 3
	done
	echo "drill 05: the metrics did not report $what within ${deadline}s" >&2
	driver_metrics | grep git_csi >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

wait_for_event() {
	local pattern="$1" deadline
	deadline=$(($(date +%s) + EVENT_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if kube get events -n "$NAMESPACE" -o yaml 2>/dev/null | grep -qE "$pattern"; then
			echo "drill 05: the events report $pattern"
			return 0
		fi
		sleep 3
	done
	echo "drill 05: no event matched $pattern within ${EVENT_DEADLINE}s" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	return 1
}

# wait_for_forge waits until the subject of the newest commit on the
# forge's main matches the pattern, which shows that the push arrived.
wait_for_forge() {
	local pattern="$1" deadline="$2" what="$3"
	local limit
	limit=$(($(date +%s) + deadline))
	while [ "$(date +%s)" -lt "$limit" ]; do
		if git -C "$FORGE" log --format=%s -1 main 2>/dev/null | grep -qE "$pattern"; then
			echo "drill 05: the forge has $what"
			return 0
		fi
		sleep 3
	done
	echo "drill 05: the forge did not hold $what within ${deadline}s" >&2
	git -C "$FORGE" log --oneline -5 main >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

# wait_for_path waits until the forge's main has the path. Use it in
# place of wait_for_forge when earlier commits have the same subject.
wait_for_path() {
	local path="$1" deadline="$2"
	local limit
	limit=$(($(date +%s) + deadline))
	while [ "$(date +%s)" -lt "$limit" ]; do
		if git -C "$FORGE" ls-tree -r --name-only main 2>/dev/null | grep -qxF "$path"; then
			echo "drill 05: the forge has $path"
			return 0
		fi
		sleep 3
	done
	echo "drill 05: the forge did not hold $path within ${deadline}s" >&2
	git -C "$FORGE" ls-tree -r --name-only main >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

echo "drill 05: the node"
kube get nodes

echo "drill 05: the forge, with hello on it"
make -C "$LAB" forge
make -C "$LAB" repo NAME=hello

echo "drill 05: the controller plugin"
kube rollout status -n "$DRIVER_NAMESPACE" deployment/git-csi-driver-controller \
	--timeout="${READY_DEADLINE}s"

echo "drill 05: the class"
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
  ignore: "*.log"
YAML

# volume creates a PersistentVolume on the host forge's repository and a
# claim that binds it and names the class.
volume() {
	kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: $1
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOncePod]
  persistentVolumeReclaimPolicy: Retain
  # The binder binds a claim to a volume only when both set the same
  # volumeAttributesClassName.
  volumeAttributesClassName: $CLASS
  csi:
    driver: git.liken.sh
    volumeHandle: $1
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
  volumeName: $1
  volumeAttributesClassName: $CLASS
  accessModes: [ReadWriteOncePod]
  resources: {requests: {storage: 1Gi}}
  storageClassName: ""
YAML
}

# writer runs a pod that mounts the claim and sleeps, so the drill can
# write into the volume with kubectl exec.
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

echo "drill 05: the volume, its claim, and the class on the claim"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
volume "$HANDLE"

echo "drill 05: a pod that writes"
writer
kube exec -n "$NAMESPACE" writer -- sh -c 'cd /config && echo one > one.yaml'

echo "drill 05: the class arms the volume"
wait_for_metric "git_csi_armed\{claim=\"config\",namespace=\"$NAMESPACE\"\} 1" \
	"$ARMED_DEADLINE" "the volume armed"
wait_for_event "GitVolumeArmed" || true

echo "drill 05: the commit reaches the forge within the quiesce"
wait_for_forge "Update 1 paths" "$PUSH_DEADLINE" "the driver's commit"
git -C "$FORGE" log --format='%an <%ae> %s' -1 main
if ! git -C "$FORGE" log --format='%an <%ae>' -1 main | grep -q "The lab <lab@liken.sh>"; then
	echo "drill 05: the commit is not by the class's author" >&2
	exit 1
fi
wait_for_event "GitVolumePushed" || true

echo "drill 05: a changed class takes effect with no restart"
# The parameters of a VolumeAttributesClass are immutable, so the drill
# creates a second class and moves the claim to it. When the next commit
# has the second class's commit.author, the running plugin read the
# change.
before="$(driver_pod)"
kube apply -f - >/dev/null <<YAML
apiVersion: storage.k8s.io/v1
kind: VolumeAttributesClass
metadata:
  name: $SECOND_CLASS
driverName: git.liken.sh
parameters:
  push.quiesce: 5s
  push.maxLatency: 5m
  commit.maxFileSize: 1Mi
  commit.author: The lab again <lab@liken.sh>
  ignore: "*.log"
YAML
kube patch pvc config -n "$NAMESPACE" --type merge \
	-p "{\"spec\":{\"volumeAttributesClassName\":\"$SECOND_CLASS\"}}" >/dev/null
kube exec -n "$NAMESPACE" writer -- sh -c \
	'cd /config && echo two > two.yaml && echo three > three.yaml'
wait_for_forge "Update 2 paths" "$PUSH_DEADLINE" "the commit the changed class pushed"
git -C "$FORGE" log --format='%an <%ae> %s' -1 main
if ! git -C "$FORGE" log --format='%an' -1 main | grep -q "The lab again"; then
	echo "drill 05: the commit is not by the changed class's author" >&2
	exit 1
fi
after="$(driver_pod)"
if [ "$before" != "$after" ]; then
	echo "drill 05: the plugin was $before and is now $after, and it must not have restarted" >&2
	exit 1
fi
echo "drill 05: the plugin is still $after, and it uses the new class"
driver_log | grep -iE "committed|pushed" | tail -n 5

echo "drill 05: the ignore list keeps a path out"
kube exec -n "$NAMESPACE" writer -- sh -c 'cd /config && echo noise > app.log'
sleep 15
if git -C "$FORGE" ls-tree -r --name-only main | grep -q '^app.log$'; then
	echo "drill 05: app.log reached the forge, and the ignore list must keep it out" >&2
	exit 1
fi
echo "drill 05: app.log is on no branch"

echo "drill 05: a file over the size guard is skipped"
kube exec -n "$NAMESPACE" writer -- sh -c \
	'cd /config && dd if=/dev/zero of=big.bin bs=1M count=2 status=none'
wait_for_metric "git_csi_skipped_files\{claim=\"config\",namespace=\"$NAMESPACE\"\} 1" \
	"$PUSH_DEADLINE" "one file over the size guard"
wait_for_event "GitVolumeFileSkipped" || true
kube get events -n "$NAMESPACE" --field-selector reason=GitVolumeFileSkipped \
	-o jsonpath='{.items[0].message}' || true
echo
if git -C "$FORGE" ls-tree -r --name-only main | grep -q '^big.bin$'; then
	echo "drill 05: big.bin reached the forge, and the size guard must keep it out" >&2
	exit 1
fi
echo "drill 05: big.bin is on no branch"

echo "drill 05: the modes and the empty directory a restore has to replay"
kube exec -n "$NAMESPACE" writer -- sh -c \
	'cd /config && echo secret > secret.yaml && chmod 600 secret.yaml && mkdir -p storage'
wait_for_path "secret.yaml" "$PUSH_DEADLINE"

echo "drill 05: a pod deleted mid-timer pushes at unpublish"
kube exec -n "$NAMESPACE" writer -- sh -c 'cd /config && echo four > four.yaml'
kube delete -n "$NAMESPACE" pod/writer --timeout="${READY_DEADLINE}s"
wait_for_path "four.yaml" "$PUSH_DEADLINE"
echo "drill 05: four.yaml reached the forge at unpublish"

echo "drill 05: the metadata ref is on the forge"
git -C "$FORGE" show refs/git-csi/metadata:metadata

echo "drill 05: the claim and the volume are deleted"
kube delete -n "$NAMESPACE" pvc/config --timeout="${CALL_TIMEOUT}"
kube delete persistentvolume "$HANDLE" --timeout="${CALL_TIMEOUT}"

echo "drill 05: a volume made again from nothing restores the tree"
volume "$RESTORE_HANDLE"
writer
kube exec -n "$NAMESPACE" writer -- ls -la /config
found="$(kube exec -n "$NAMESPACE" writer -- stat -c '%a' /config/secret.yaml)"
if [ "$(echo "$found" | tr -d '[:space:]')" != "600" ]; then
	echo "drill 05: secret.yaml is $found on the restored volume, want 600" >&2
	exit 1
fi
if ! kube exec -n "$NAMESPACE" writer -- test -d /config/storage; then
	echo "drill 05: the empty directory did not return to the restored volume" >&2
	exit 1
fi
echo "drill 05: the restored tree has secret.yaml at 600 and the empty directory"

echo "drill 05: the plugin's memory"
kube top pod -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node ||
	echo "drill 05: no metrics server in this lab"

echo "drill 05: passed"
