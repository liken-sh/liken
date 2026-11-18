#!/usr/bin/env bash
# Drill 04: the sweep deletes a volume's copy from the node after its
# PersistentVolume is deleted, and does not delete it while a pod mounts
# the volume.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

IMAGE="${IMAGE:-debian:sid-slim}"
NAMESPACE=drill-04
HANDLE=drill-04-store
STORE=/host/var/lib/liken/pod-storage/per-node

READY_DEADLINE="${READY_DEADLINE:-240}"
GONE_DEADLINE="${GONE_DEADLINE:-180}"
SWEEP_DEADLINE="${SWEEP_DEADLINE:-180}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$HANDLE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete pod store-reader --ignore-not-found --wait=false >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT

# store_reader runs a privileged pod with the node's root filesystem
# mounted at /host, so the drill can look at the store on the node.
store_reader() {
	kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: store-reader
  namespace: default
spec:
  hostNetwork: true
  hostPID: true
  nodeSelector: {liken.sh/machine: "true"}
  tolerations: [{operator: Exists}]
  restartPolicy: Never
  containers:
    - name: store-reader
      image: $IMAGE
      command: ["sh", "-c", "sleep infinity"]
      securityContext: {privileged: true}
      volumeMounts:
        - name: host
          mountPath: /host
          mountPropagation: HostToContainer
  volumes:
    - {name: host, hostPath: {path: /}}
YAML
	kube wait --for=condition=Ready pod/store-reader --timeout="${READY_DEADLINE}s"
}

# copy_is_there succeeds while the volume's copy exists in the store.
copy_is_there() {
	kube exec store-reader -- test -d "$STORE/copies/$HANDLE" 2>/dev/null
}

# wait_for_sweep waits until the copy is gone, and fails after
# SWEEP_DEADLINE seconds.
wait_for_sweep() {
	local deadline
	deadline=$(($(date +%s) + SWEEP_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if ! copy_is_there; then
			echo "drill 04: the copy is gone from the store"
			return 0
		fi
		sleep 3
	done
	echo "drill 04: the copy is still on the node after ${SWEEP_DEADLINE}s" >&2
	kube exec store-reader -- ls -l "$STORE/copies" >&2 || true
	kube logs -n liken-system -l app=per-node-csi-driver-node -c driver --tail=40 >&2 || true
	echo "drill 04: FAIL" >&2
	return 1
}

echo "drill 04: the node"
kube get nodes

echo "drill 04: a pod on the node that reads the store"
store_reader

echo "drill 04: the volume, its claim, and a pod that fills it"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
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
---
apiVersion: v1
kind: Pod
metadata:
  name: writer
  namespace: $NAMESPACE
spec:
  restartPolicy: Never
  tolerations:
    - operator: Exists
  containers:
    - name: writer
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
kube wait -n "$NAMESPACE" --for=condition=Ready pod/writer --timeout="${READY_DEADLINE}s"
kube exec -n "$NAMESPACE" writer -- sh -c 'echo hello > /data/greeting'

echo "drill 04: the copy is on the node"
kube exec store-reader -- ls -l "$STORE/copies/$HANDLE"

echo "drill 04: the sweep keeps a copy that a pod mounts"
# The lab runs the sweep every 15 seconds (lab/sweep-every.yaml), so
# the copy has been through at least one sweep after 30 seconds.
sleep 30
if ! copy_is_there; then
	echo "drill 04: the sweep deleted the copy while a pod mounted it" >&2
	echo "drill 04: FAIL" >&2
	exit 1
fi
echo "drill 04: the mounted copy is still on the node"

echo "drill 04: delete the pod, the claim, and the volume"
kube delete -n "$NAMESPACE" pod writer --timeout="${GONE_DEADLINE}s"
kube delete -n "$NAMESPACE" persistentvolumeclaim store --timeout="${GONE_DEADLINE}s"
kube delete persistentvolume "$HANDLE" --timeout="${GONE_DEADLINE}s"

echo "drill 04: the sweep deletes the copy"
wait_for_sweep

echo "drill 04: PASS"
