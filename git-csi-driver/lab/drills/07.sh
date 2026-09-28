#!/usr/bin/env bash
# Drill 07. A ReadOnlyMany claim on the forge's repository. Two pods in
# one namespace read the tree, and a push reaches both inside pull. A
# write in a pod fails, and a pod that does not ask for read-only is
# refused. The driver pod is deleted, and the new one takes
# the volume back and follows the next push. The last pod's exit removes
# the tree from the node. With the forge stopped, a claim with
# offline: refuse stays refused, and the forge's return starts its pod.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

# The forge is on the host, and the guest reaches it at the
# user-mode network's gateway.
GUEST_FORGE="git://10.0.2.2:9418/hello.git"
HOST_FORGE="$LAB/forge/hello.git"

# The reader image is one the lab already pulled, so no drill waits on
# a registry.
IMAGE="${IMAGE:-debian:12-slim}"
DRIVER_NAMESPACE="${DRIVER_NAMESPACE:-liken-system}"
NAMESPACE=drill-07
PULL="${PULL:-15s}"
# The store on the node keeps a tree per handle, and the forge is
# reseeded on every run, so a handle carries a run's own stamp.
RUN="$(date +%s)"
HANDLE="drill-07-$RUN"
OFFLINE_HANDLE="drill-07-offline-$RUN"
# The driver's store on the node. The DaemonSet mounts it from the
# pod-storage partition.
STORE=/var/lib/liken/pod-storage/git-csi

# Every wait in this drill has a deadline, in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
REACH_DEADLINE="${REACH_DEADLINE:-180}"
EVENT_DEADLINE="${EVENT_DEADLINE:-180}"
STORE_DEADLINE="${STORE_DEADLINE:-180}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

# Cleanup leaves the cluster and the host as the drill found them,
# however the drill ends.
cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$HANDLE" "$OFFLINE_HANDLE" \
		--ignore-not-found --wait=false >/dev/null 2>&1 || true
	rm -rf "$LAB/guests/drill-07"
	make -C "$LAB" forge >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT

# Driver_pod is the node plugin's pod on the one node this lab runs.
driver_pod() {
	kube get pods -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

# Store_volumes is what the node's store holds under volumes/.
store_volumes() {
	local pod
	pod="$(driver_pod)"
	test -n "$pod" || return 0
	kube exec -n "$DRIVER_NAMESPACE" "$pod" -c driver -- \
		sh -c "ls -1 $STORE/volumes 2>/dev/null" 2>/dev/null || true
}

# Claim writes the PersistentVolume and the claim that binds it, both
# ReadOnlyMany.
claim() {
	local handle="$1" name="$2" offline="$3"
	kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: $handle
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadOnlyMany]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: ""
  csi:
    driver: git.liken.sh
    volumeHandle: $handle
    readOnly: true
    volumeAttributes:
      url: $GUEST_FORGE
      ref: main
      pull: $PULL
      offline: $offline
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $name
  namespace: $NAMESPACE
spec:
  accessModes: [ReadOnlyMany]
  storageClassName: ""
  volumeName: $handle
  resources: {requests: {storage: 1Gi}}
YAML
}

# Reader makes one pod that mounts the claim read-only. A pod has to ask
# for read-only, or the driver refuses it, because the container runtime
# binds the volume into the pod read-write otherwise.
reader() {
	local name="$1" claimName="$2" readOnly="${3:-true}"
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
    - name: reader
      image: $IMAGE
      command: ["sh", "-c", "sleep infinity"]
      volumeMounts:
        - name: hello
          mountPath: /hello
  volumes:
    - name: hello
      persistentVolumeClaim:
        claimName: $claimName
        readOnly: $readOnly
YAML
}

# Greeting is what the pod reads out of the mounted checkout, or nothing.
greeting() {
	local name="$1"
	kube exec -n "$NAMESPACE" "$name" -- \
		sh -c 'sed -n "s/^greeting: //p" /hello/greeting.yaml' 2>/dev/null || true
}

# Wait_for_greeting waits until the pod reads the greeting, or fails on
# the deadline.
wait_for_greeting() {
	local name="$1" want="$2" deadline
	deadline=$(($(date +%s) + REACH_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if [ "$(greeting "$name")" = "$want" ]; then
			echo "drill 07: $name reads $want"
			return 0
		fi
		sleep 3
	done
	echo "drill 07: $name did not read $want within ${REACH_DEADLINE}s" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	return 1
}

# Wait_for_event waits until the events of the object carry the reason,
# or fails on the deadline.
wait_for_event() {
	local selector="$1" pattern="$2" deadline
	deadline=$(($(date +%s) + EVENT_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if kube get events -n "$NAMESPACE" --field-selector "$selector" -o yaml 2>/dev/null |
			grep -qE "$pattern"; then
			echo "drill 07: $selector reported $pattern"
			return 0
		fi
		sleep 3
	done
	echo "drill 07: $selector did not report $pattern within ${EVENT_DEADLINE}s" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	return 1
}

# Push writes the greeting on the forge and pushes it.
push() {
	local greeting="$1"
	rm -rf "$LAB/guests/drill-07"
	git clone --quiet "$HOST_FORGE" "$LAB/guests/drill-07"
	sed -i "s/^greeting: .*/greeting: $greeting/" "$LAB/guests/drill-07/greeting.yaml"
	git -C "$LAB/guests/drill-07" add greeting.yaml
	git -C "$LAB/guests/drill-07" -c user.name=lab -c user.email=lab@liken.sh \
		commit --quiet -m "$greeting"
	git -C "$LAB/guests/drill-07" push --quiet origin main
}

echo "drill 07: the node"
kube get nodes

echo "drill 07: the forge, with hello on it"
make -C "$LAB" forge
make -C "$LAB" repo NAME=hello

echo "drill 07: a ReadOnlyMany claim and two pods that mount it"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
claim "$HANDLE" hello refuse
reader reader-a hello
reader reader-b hello
for name in reader-a reader-b; do
	kube wait -n "$NAMESPACE" --for=condition=Ready "pod/$name" --timeout="${READY_DEADLINE}s"
done
wait_for_greeting reader-a hello
wait_for_greeting reader-b hello

echo "drill 07: a push to the forge reaches both pods inside $PULL"
push goodbye
wait_for_greeting reader-a goodbye
wait_for_greeting reader-b goodbye

echo "drill 07: a write in one pod fails"
if kube exec -n "$NAMESPACE" reader-a -- sh -c 'echo no > /hello/written.yaml' 2>/dev/null; then
	echo "drill 07: the pod wrote in a read-only mount, and it must not" >&2
	exit 1
fi
echo "drill 07: the mount is read-only"

echo "drill 07: a pod that does not ask for read-only is refused"
reader writer hello false
wait_for_event "involvedObject.kind=Pod,involvedObject.name=writer" 'GitVolumeRefused'
if kube get -n "$NAMESPACE" pod/writer -o jsonpath='{.status.phase}' | grep -q Running; then
	echo "drill 07: the writer pod is running, and it must not be" >&2
	exit 1
fi
kube delete pod -n "$NAMESPACE" writer --wait=false >/dev/null

echo "drill 07: the driver's node pod is deleted"
kube delete pod -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node --timeout="${READY_DEADLINE}s"
kube rollout status -n "$DRIVER_NAMESPACE" daemonset/git-csi-driver-node \
	--timeout="${READY_DEADLINE}s"
wait_for_greeting reader-a goodbye
wait_for_greeting reader-b goodbye

echo "drill 07: a second push reaches both pods through the new driver pod"
push hello-again
wait_for_greeting reader-a hello-again
wait_for_greeting reader-b hello-again

echo "drill 07: both pods are deleted, and the store holds no tree"
kube delete -n "$NAMESPACE" pod/reader-a pod/reader-b --timeout="${READY_DEADLINE}s"
deadline=$(($(date +%s) + STORE_DEADLINE))
while [ "$(date +%s)" -lt "$deadline" ]; do
	if ! store_volumes | grep -qx "$HANDLE"; then
		echo "drill 07: the store holds no tree for $HANDLE"
		break
	fi
	sleep 3
done
if store_volumes | grep -qx "$HANDLE"; then
	echo "drill 07: the store still holds a tree for $HANDLE after ${STORE_DEADLINE}s" >&2
	store_volumes >&2
	exit 1
fi

echo "drill 07: the forge stops, and a claim with offline refuse stays refused"
make -C "$LAB" forge-stop
claim "$OFFLINE_HANDLE" offline refuse
reader reader-c offline
wait_for_event "involvedObject.kind=PersistentVolumeClaim,involvedObject.name=offline" \
	'GitVolumeRefused'
if kube get -n "$NAMESPACE" pod/reader-c -o jsonpath='{.status.phase}' | grep -q Running; then
	echo "drill 07: the refused pod is running, and it must not be" >&2
	exit 1
fi

echo "drill 07: the forge returns, and the refused pod starts"
make -C "$LAB" forge
kube wait -n "$NAMESPACE" --for=condition=Ready pod/reader-c --timeout="${READY_DEADLINE}s"
wait_for_greeting reader-c hello-again

# kubectl top needs a metrics server. A lab without one still passes.
echo "drill 07: the plugin's memory"
kube top pod -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node ||
	echo "drill 07: no metrics server in this lab"

echo "drill 07: passed"
