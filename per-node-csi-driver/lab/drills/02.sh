#!/usr/bin/env bash
# Drill 02: pods on one node share the node's copy. Two pods on one node
# mount the same claim, and each reads what the other wrote. The node
# plugin restarts under them and keeps both holds. When the first pod
# goes, the second keeps the copy and its files, and a third pod reads
# what the second wrote.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

IMAGE="${IMAGE:-debian:sid-slim}"
NAMESPACE=drill-02
HANDLE=drill-02-store

# Every wait in this drill has a deadline, in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
GONE_DEADLINE="${GONE_DEADLINE:-180}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

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

# holder is one pod that mounts the claim and waits until it is ready.
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

# reads_back fails the drill unless the pod reads the wanted text from
# the copy.
reads_back() {
	local pod="$1" want="$2" read_back
	read_back="$(kube exec -n "$NAMESPACE" "$pod" -- cat /data/greeting)"
	if [ "$read_back" != "$want" ]; then
		echo "drill 02: the $pod pod read '$read_back', want $want" >&2
		echo "drill 02: FAIL" >&2
		exit 1
	fi
	echo "drill 02: the $pod pod reads $want"
}

echo "drill 02: the node"
kube get nodes

echo "drill 02: the volume and its claim"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
volume

echo "drill 02: the first pod takes the copy and writes into it"
holder first
kube exec -n "$NAMESPACE" first -- sh -c 'echo hello > /data/greeting'

echo "drill 02: a second pod on the same node mounts the same copy"
holder second
reads_back second hello
kube exec -n "$NAMESPACE" second -- sh -c 'echo world > /data/greeting'
reads_back first world

echo "drill 02: the node plugin restarts under both pods"
kube rollout restart -n liken-system daemonset/per-node-csi-driver-node >/dev/null
kube rollout status -n liken-system daemonset/per-node-csi-driver-node --timeout="${READY_DEADLINE}s"
reads_back first world
reads_back second world

echo "drill 02: the first pod goes, and the second keeps the copy"
kube delete -n "$NAMESPACE" pod first --timeout="${GONE_DEADLINE}s"
reads_back second world

echo "drill 02: the second pod goes, and a third pod reads what it wrote"
kube delete -n "$NAMESPACE" pod second --timeout="${GONE_DEADLINE}s"
holder third
reads_back third world

echo "drill 02: PASS"
