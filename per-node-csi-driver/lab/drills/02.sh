#!/usr/bin/env bash
# Drill 02: one pod per node per volume. Two pods on one node ask for
# the same claim. The second stays Pending and carries the refusal on
# its events. Deleting the refused pod leaves the first pod's copy in
# place, and a third pod is refused the same way. When the first pod
# goes, the third starts and reads what the first wrote.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

IMAGE="${IMAGE:-debian:12-slim}"
NAMESPACE=drill-02
HANDLE=drill-02-store

# Every wait in this drill has a deadline, in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
EVENT_DEADLINE="${EVENT_DEADLINE:-240}"
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

# holder is one pod that mounts the claim and waits. It does not wait
# for the pod to be ready, because the second pod never will be.
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
}

# wait_for_event waits until the namespace's events carry the pattern,
# or fails on the deadline.
wait_for_event() {
	local pattern="$1" deadline
	deadline=$(($(date +%s) + EVENT_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if kube get events -n "$NAMESPACE" -o yaml 2>/dev/null | grep -qE "$pattern"; then
			echo "drill 02: the events report $pattern"
			return 0
		fi
		sleep 3
	done
	echo "drill 02: no event matched $pattern within ${EVENT_DEADLINE}s" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	echo "drill 02: FAIL" >&2
	return 1
}

echo "drill 02: the node"
kube get nodes

echo "drill 02: the volume and its claim"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
volume

echo "drill 02: the first pod takes the copy"
holder first
kube wait -n "$NAMESPACE" --for=condition=Ready pod/first --timeout="${READY_DEADLINE}s"

echo "drill 02: the second pod asks for the same claim"
holder second
wait_for_event "PerNodeVolumeHeld"

phase="$(kube get -n "$NAMESPACE" pod/second -o jsonpath='{.status.phase}')"
if [ "$phase" != "Pending" ]; then
	echo "drill 02: the second pod is $phase, want Pending" >&2
	echo "drill 02: FAIL" >&2
	exit 1
fi
echo "drill 02: the second pod stays $phase"

echo "drill 02: the first pod writes into the copy"
kube exec -n "$NAMESPACE" first -- sh -c 'echo hello > /data/greeting'

echo "drill 02: the refused pod goes, and the kubelet unpublishes its target"
kube delete -n "$NAMESPACE" pod second --timeout="${GONE_DEADLINE}s"

echo "drill 02: the first pod still holds the copy it wrote"
read_back="$(kube exec -n "$NAMESPACE" first -- cat /data/greeting)"
if [ "$read_back" != "hello" ]; then
	echo "drill 02: the first pod read '$read_back', want hello" >&2
	echo "drill 02: FAIL" >&2
	exit 1
fi

echo "drill 02: a third pod is refused the same way"
holder third
wait_for_event "PerNodeVolumeHeld"
phase="$(kube get -n "$NAMESPACE" pod/third -o jsonpath='{.status.phase}')"
if [ "$phase" != "Pending" ]; then
	echo "drill 02: the third pod is $phase, want Pending" >&2
	echo "drill 02: FAIL" >&2
	exit 1
fi
echo "drill 02: the third pod stays $phase"

echo "drill 02: the first pod goes"
kube delete -n "$NAMESPACE" pod first --timeout="${GONE_DEADLINE}s"

echo "drill 02: the third pod starts, and reads what the first pod wrote"
kube wait -n "$NAMESPACE" --for=condition=Ready pod/third --timeout="${READY_DEADLINE}s"
read_back="$(kube exec -n "$NAMESPACE" third -- cat /data/greeting)"
if [ "$read_back" != "hello" ]; then
	echo "drill 02: the third pod read '$read_back', want hello" >&2
	echo "drill 02: FAIL" >&2
	exit 1
fi

echo "drill 02: PASS"
