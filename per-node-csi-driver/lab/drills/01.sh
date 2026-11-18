#!/usr/bin/env bash
# Drill 01: data in a per-node volume survives the deletion of the pod
# that wrote it. A pod writes a file into the volume and is deleted. A
# second pod on the same node mounts the same claim and reads the file.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

IMAGE="${IMAGE:-debian:sid-slim}"
NAMESPACE=drill-01
HANDLE=drill-01-store

# The deadlines are in seconds. CALL_TIMEOUT limits each kubectl request.
READY_DEADLINE="${READY_DEADLINE:-240}"
GONE_DEADLINE="${GONE_DEADLINE:-180}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

# cleanup deletes the drill's namespace and PersistentVolume on every
# exit, so a failed run leaves nothing behind.
cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$HANDLE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT

volume() {
	kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: $HANDLE
spec:
  storageClassName: per-node
  accessModes: [ReadWriteMany]
  capacity: {storage: 1Gi}
  persistentVolumeReclaimPolicy: Retain
  claimRef:
    namespace: $NAMESPACE
    name: store
  csi:
    driver: per-node.liken.sh
    volumeHandle: $HANDLE
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: store
  namespace: $NAMESPACE
spec:
  storageClassName: per-node
  accessModes: [ReadWriteMany]
  resources: {requests: {storage: 1Gi}}
YAML
}

# start_pod starts a pod that mounts the claim and waits until it is Ready.
start_pod() {
	local name="$1"
	kube apply -n "$NAMESPACE" -f - >/dev/null <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: $name
spec:
  restartPolicy: Never
  tolerations:
    - operator: Exists
  containers:
    - name: sleeper
      image: $IMAGE
      command: ["sh", "-c", "sleep infinity"]
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: store
YAML
	kube wait -n "$NAMESPACE" --for=condition=Ready "pod/$name" --timeout="${READY_DEADLINE}s"
}

echo "drill 01: the node"
kube get nodes

echo "drill 01: the volume and its claim"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
volume

echo "drill 01: the first pod writes hello"
start_pod writer
kube exec -n "$NAMESPACE" writer -- sh -c 'echo hello > /data/greeting'

echo "drill 01: the first pod is deleted"
kube delete -n "$NAMESPACE" pod writer --timeout="${GONE_DEADLINE}s"

echo "drill 01: the second pod reads hello back"
start_pod reader
read_back="$(kube exec -n "$NAMESPACE" reader -- cat /data/greeting)"
if [ "$read_back" != "hello" ]; then
	echo "drill 01: the second pod read '$read_back', want hello" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	echo "drill 01: FAIL" >&2
	exit 1
fi

echo "drill 01: PASS"
