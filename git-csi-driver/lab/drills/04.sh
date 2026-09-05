#!/usr/bin/env bash
# Drill 04. A writeable volume against the host forge: the pod
# writes three files, the driver reports them pending and unarmed, a
# claim with ReadWriteOnce is refused, the files survive the pod, and a
# class arms the volume.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

# The forge is on the host, and the guest reaches it at the
# user-mode network's gateway.
GUEST_FORGE="git://10.0.2.2:9418/hello.git"

# The writer image is one the lab already pulled, so no drill
# waits on a registry.
IMAGE="${IMAGE:-debian:12-slim}"
DRIVER_NAMESPACE="${DRIVER_NAMESPACE:-liken-system}"
NAMESPACE=drill-04
HANDLE=drill-04
CLASS=drill-04

# Every wait in this drill has a deadline, in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
PENDING_DEADLINE="${PENDING_DEADLINE:-180}"
EVENT_DEADLINE="${EVENT_DEADLINE:-180}"
ARMED_DEADLINE="${ARMED_DEADLINE:-180}"
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
	return "$status"
}
trap cleanup EXIT

# Driver_log is what the node plugin wrote about this volume.
driver_log() {
	kube logs -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node -c driver --tail=200 2>/dev/null || true
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
			echo "drill 04: the metrics report $what"
			return 0
		fi
		sleep 3
	done
	echo "drill 04: the metrics did not report $what within ${deadline}s" >&2
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
			echo "drill 04: the events report $pattern"
			return 0
		fi
		sleep 3
	done
	echo "drill 04: no event matched $pattern within ${EVENT_DEADLINE}s" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	return 1
}

# Wait_for_log waits until the driver's log carries the pattern.
wait_for_log() {
	local pattern="$1" deadline="$2" what="$3"
	local limit
	limit=$(($(date +%s) + deadline))
	while [ "$(date +%s)" -lt "$limit" ]; do
		if driver_log | grep -qE "$pattern"; then
			echo "drill 04: the driver reports $what"
			return 0
		fi
		sleep 3
	done
	echo "drill 04: the driver did not report $what within ${deadline}s" >&2
	driver_log | tail -n 40 >&2
	return 1
}

echo "drill 04: the node"
kube get nodes

echo "drill 04: the forge, with hello on it"
make -C "$LAB" forge
make -C "$LAB" repo NAME=hello

echo "drill 04: the volume and its claim"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: $HANDLE
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOncePod]
  persistentVolumeReclaimPolicy: Retain
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
  accessModes: [ReadWriteOncePod]
  resources: {requests: {storage: 1Gi}}
  storageClassName: ""
YAML

# The writer holds the volume open and writes three files into it.
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
}

echo "drill 04: a pod that writes three files"
writer
kube wait -n "$NAMESPACE" --for=condition=Ready pod/writer --timeout="${READY_DEADLINE}s"
kube exec -n "$NAMESPACE" writer -- sh -c \
	'cd /config && echo one > one.yaml && echo two > two.yaml && echo three > three.yaml'
kube exec -n "$NAMESPACE" writer -- ls -1 /config

echo "drill 04: the driver reports three paths pending, and unarmed"
wait_for_log 'the tree holds work' "$PENDING_DEADLINE" "the work in the tree"
wait_for_metric "git_csi_pending_paths\{claim=\"config\",namespace=\"$NAMESPACE\"\} 3" \
	"$PENDING_DEADLINE" "three paths pending"
wait_for_metric "git_csi_armed\{claim=\"config\",namespace=\"$NAMESPACE\"\} 0" \
	"$PENDING_DEADLINE" "the volume unarmed"
wait_for_event 'GitVolumePending' || true

echo "drill 04: the volume conditions the kubelet holds"
kube get --raw "/api/v1/nodes/node-1/proxy/metrics" 2>/dev/null |
	grep 'kubelet_volume_stats_health_status_abnormal' ||
	echo "drill 04: the kubelet has posted no volume stats yet"

echo "drill 04: a claim with ReadWriteOnce is refused"
kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: $HANDLE-rwo
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOnce]
  persistentVolumeReclaimPolicy: Retain
  csi:
    driver: git.liken.sh
    volumeHandle: $HANDLE-rwo
    volumeAttributes:
      url: $GUEST_FORGE
      ref: main
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: shared
  namespace: $NAMESPACE
spec:
  volumeName: $HANDLE-rwo
  accessModes: [ReadWriteOnce]
  resources: {requests: {storage: 1Gi}}
  storageClassName: ""
---
apiVersion: v1
kind: Pod
metadata:
  name: sharer
  namespace: $NAMESPACE
spec:
  restartPolicy: Never
  tolerations:
    - operator: Exists
  containers:
    - name: sharer
      image: $IMAGE
      command: ["sh", "-c", "sleep infinity"]
      volumeMounts:
        - name: config
          mountPath: /config
  volumes:
    - name: config
      persistentVolumeClaim:
        claimName: shared
YAML
wait_for_event 'SINGLE_NODE_SINGLE_WRITER|ReadWriteOncePod'
if kube get -n "$NAMESPACE" pod/sharer -o jsonpath='{.status.phase}' | grep -q Running; then
	echo "drill 04: the ReadWriteOnce pod is running, and it must not be" >&2
	exit 1
fi
kube delete -n "$NAMESPACE" pod/sharer --ignore-not-found --timeout="${CALL_TIMEOUT}"
kube delete -n "$NAMESPACE" pvc/shared --ignore-not-found --timeout="${CALL_TIMEOUT}"
kube delete persistentvolume "$HANDLE-rwo" --ignore-not-found --timeout="${CALL_TIMEOUT}"

echo "drill 04: the pod is deleted and made again, and finds its files"
kube delete -n "$NAMESPACE" pod/writer --timeout="${READY_DEADLINE}s"
writer
kube wait -n "$NAMESPACE" --for=condition=Ready pod/writer --timeout="${READY_DEADLINE}s"
found="$(kube exec -n "$NAMESPACE" writer -- sh -c 'ls -1 /config/*.yaml | wc -l')"
if [ "$(echo "$found" | tr -d '[:space:]')" != "3" ]; then
	echo "drill 04: the new pod found $found files, want 3" >&2
	kube exec -n "$NAMESPACE" writer -- ls -la /config >&2 || true
	exit 1
fi
echo "drill 04: the three files survived the pod"

echo "drill 04: a class arms the volume"
# The API server requires one parameter on a class, so the class
# names the quiesce plan 05 reads.
kube apply -f - >/dev/null <<YAML
apiVersion: storage.k8s.io/v1
kind: VolumeAttributesClass
metadata:
  name: $CLASS
driverName: git.liken.sh
parameters:
  push.quiesce: 30s
YAML
kube patch -n "$NAMESPACE" pvc/config --type=merge \
	-p "{\"spec\":{\"volumeAttributesClassName\":\"$CLASS\"}}"
wait_for_metric "git_csi_armed\{claim=\"config\",namespace=\"$NAMESPACE\"\} 1" \
	"$ARMED_DEADLINE" "the volume armed"
wait_for_event "GitVolumeArmed" || true

echo "drill 04: the plugin's memory"
kube top pod -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node ||
	echo "drill 04: no metrics server in this lab"

echo "drill 04: passed"
