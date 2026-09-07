#!/usr/bin/env bash
# Drill 05: the driver reports its bytes. The kubelet's summary carries
# the copy's used bytes, because the driver answers NodeGetVolumeStats,
# and the plugin's metrics port answers per_node_copy_bytes for the
# same volume.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

IMAGE="${IMAGE:-debian:12-slim}"
NODE="${NODE:-node-1}"
DRIVER_NAMESPACE="${DRIVER_NAMESPACE:-liken-system}"
NAMESPACE=drill-05
HANDLE=drill-05-store

READY_DEADLINE="${READY_DEADLINE:-240}"
# The kubelet polls a driver's volume stats about once a minute, so
# this deadline is minutes, not seconds.
STATS_DEADLINE="${STATS_DEADLINE:-360}"
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

# used_bytes is what the kubelet's summary says the claim holds, and 0
# when the summary does not carry it yet.
used_bytes() {
	kube get --raw "/api/v1/nodes/$NODE/proxy/stats/summary" 2>/dev/null |
		python3 -c '
import json, sys
summary = json.load(sys.stdin)
for pod in summary.get("pods", []):
    for volume in pod.get("volume", []):
        if volume.get("pvcRef", {}).get("name") == "store":
            print(volume.get("usedBytes", 0))
            raise SystemExit
print(0)
'
}

# wait_for_stats waits until the summary reports bytes for the claim.
wait_for_stats() {
	local deadline used
	deadline=$(($(date +%s) + STATS_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		used="$(used_bytes || echo 0)"
		if [ "${used:-0}" -gt 0 ]; then
			echo "drill 05: the kubelet reports $used bytes used"
			return 0
		fi
		sleep 5
	done
	echo "drill 05: the kubelet reported no bytes within ${STATS_DEADLINE}s" >&2
	echo "drill 05: FAIL" >&2
	return 1
}

echo "drill 05: the node"
kube get nodes

echo "drill 05: the volume, its claim, and a pod that fills it"
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

echo "drill 05: the pod writes a megabyte"
kube exec -n "$NAMESPACE" writer -- \
	dd if=/dev/zero of=/data/block bs=1024 count=1024 status=none

echo "drill 05: the kubelet's summary carries the copy"
wait_for_stats

echo "drill 05: the plugin's metrics port answers"
plugin="$(kube get pods -n "$DRIVER_NAMESPACE" -l app=per-node-csi-driver-node \
	-o jsonpath='{.items[0].metadata.name}')"
metrics="$(kube get --raw \
	"/api/v1/namespaces/$DRIVER_NAMESPACE/pods/$plugin:9808/proxy/metrics")"
if ! echo "$metrics" | grep -q "per_node_copy_bytes{volume=\"$HANDLE\"}"; then
	echo "drill 05: the metrics port carries no gauge for $HANDLE" >&2
	echo "$metrics" | grep per_node_copy_bytes >&2 || true
	echo "drill 05: FAIL" >&2
	exit 1
fi
echo "$metrics" | grep "per_node_copy_bytes{volume=\"$HANDLE\"}"

echo "drill 05: PASS"
