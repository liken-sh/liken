#!/usr/bin/env bash
# Drill 03: a bad handle is refused. A PersistentVolume names a handle
# the driver cannot put under the store. The pod never runs, and its
# events say why.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

IMAGE="${IMAGE:-debian:12-slim}"
NAMESPACE=drill-03
VOLUME=drill-03-store
# A handle with a / in it. One path element under the store can never
# hold a separator.
HANDLE='Bad/Handle'

EVENT_DEADLINE="${EVENT_DEADLINE:-240}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$VOLUME" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT

wait_for_event() {
	local pattern="$1" deadline
	deadline=$(($(date +%s) + EVENT_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if kube get events -n "$NAMESPACE" -o yaml 2>/dev/null | grep -qE "$pattern"; then
			echo "drill 03: the events report $pattern"
			return 0
		fi
		sleep 3
	done
	echo "drill 03: no event matched $pattern within ${EVENT_DEADLINE}s" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	echo "drill 03: FAIL" >&2
	return 1
}

echo "drill 03: the node"
kube get nodes

echo "drill 03: a volume whose handle the driver refuses"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: $VOLUME
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
    volumeHandle: "$HANDLE"
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
  name: reader
  namespace: $NAMESPACE
spec:
  restartPolicy: Never
  tolerations:
    - operator: Exists
  containers:
    - name: reader
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

echo "drill 03: the pod's events say why"
wait_for_event "PerNodeVolumeRefused"
wait_for_event "DNS-1123 subdomain name"

phase="$(kube get -n "$NAMESPACE" pod/reader -o jsonpath='{.status.phase}')"
if [ "$phase" = "Running" ]; then
	echo "drill 03: the pod is running, and it must not be" >&2
	echo "drill 03: FAIL" >&2
	exit 1
fi
echo "drill 03: the pod stays $phase"

kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp | grep PerNode

echo "drill 03: PASS"
