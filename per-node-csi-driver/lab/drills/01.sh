#!/usr/bin/env bash
# Drill 01: the copy outlives its pod. A pod writes a file into a
# per-node volume and is deleted. A second pod on the same node, with
# the same claim, reads the file back.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

# The writer image is one the lab already pulled, so no drill waits on
# a registry.
IMAGE="${IMAGE:-debian:12-slim}"
NAMESPACE=drill-01
HANDLE=drill-01-store

# Every wait in this drill has a deadline, in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
GONE_DEADLINE="${GONE_DEADLINE:-180}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

# cleanup leaves the cluster as the drill found it, however the drill
# ends.
cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$HANDLE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT

# volume writes the PersistentVolume and the claim that binds it.
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

# holder is one pod that mounts the claim and waits.
holder() {
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
    - name: holder
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
holder writer
kube exec -n "$NAMESPACE" writer -- sh -c 'echo hello > /data/greeting'

echo "drill 01: the first pod goes"
kube delete -n "$NAMESPACE" pod writer --timeout="${GONE_DEADLINE}s"

echo "drill 01: the second pod reads hello back"
holder reader
read_back="$(kube exec -n "$NAMESPACE" reader -- cat /data/greeting)"
if [ "$read_back" != "hello" ]; then
	echo "drill 01: the second pod read '$read_back', want hello" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	echo "drill 01: FAIL" >&2
	exit 1
fi

echo "drill 01: PASS"
