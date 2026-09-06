#!/usr/bin/env bash
# Drill 03. Two pods in two namespaces read one repository. A push to
# the forge reaches both inside pull. With the forge stopped, a volume
# with offline: allowStale publishes the node's copy and one with
# offline: refuse stays refused. The forge's return clears both.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

# The forge is on the host. The guest reaches it at the user-mode
# network's gateway.
GUEST_FORGE="git://10.0.2.2:9418/hello.git"
HOST_FORGE="$LAB/forge/hello.git"

# The reader image is one the lab already pulled, so no drill waits on
# a registry.
IMAGE="${IMAGE:-debian:12-slim}"
DRIVER_NAMESPACE="${DRIVER_NAMESPACE:-liken-system}"
PULL="${PULL:-15s}"

# Every wait in this drill has a deadline, in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
REACH_DEADLINE="${REACH_DEADLINE:-180}"
EVENT_DEADLINE="${EVENT_DEADLINE:-180}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

NAMESPACES=(drill-03-a drill-03-b drill-03-stale drill-03-refuse)
WORK="$LAB/guests/drill-03"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

# cleanup leaves the cluster and the host as the drill found them,
# however the drill ends.
cleanup() {
	local status=$?
	for namespace in "${NAMESPACES[@]}"; do
		kube delete namespace "$namespace" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	done
	rm -rf "$WORK"
	make -C "$LAB" forge >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT

# reader makes a pod in its own namespace with one inline volume of the
# repository.
reader() {
	local namespace="$1" offline="$2"
	kube create namespace "$namespace" >/dev/null
	kube apply -n "$namespace" -f - >/dev/null <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: reader
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
          readOnly: true
  volumes:
    - name: hello
      csi:
        driver: git.liken.sh
        readOnly: true
        volumeAttributes:
          url: $GUEST_FORGE
          ref: main
          pull: $PULL
          offline: $offline
YAML
}

# greeting is what the pod reads out of the mounted checkout, or nothing.
greeting() {
	local namespace="$1"
	kube exec -n "$namespace" reader -- \
		sh -c 'sed -n "s/^greeting: //p" /hello/greeting.yaml' 2>/dev/null || true
}

# wait_for_greeting waits until the pod reads the greeting, or fails on
# the deadline.
wait_for_greeting() {
	local namespace="$1" want="$2" deadline
	deadline=$(($(date +%s) + REACH_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if [ "$(greeting "$namespace")" = "$want" ]; then
			echo "drill 03: $namespace reads $want"
			return 0
		fi
		sleep 3
	done
	echo "drill 03: $namespace did not read $want within ${REACH_DEADLINE}s" >&2
	kube get events -n "$namespace" --sort-by=.lastTimestamp >&2 || true
	return 1
}

# wait_for_event waits until the pod's events carry the reason, or fails
# on the deadline.
wait_for_event() {
	local namespace="$1" pattern="$2" deadline
	deadline=$(($(date +%s) + EVENT_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if kube get events -n "$namespace" -o yaml 2>/dev/null | grep -qE "$pattern"; then
			echo "drill 03: $namespace reported $pattern"
			return 0
		fi
		sleep 3
	done
	echo "drill 03: $namespace did not report $pattern within ${EVENT_DEADLINE}s" >&2
	kube get events -n "$namespace" --sort-by=.lastTimestamp >&2 || true
	return 1
}

echo "drill 03: the node"
kube get nodes

echo "drill 03: the forge, with hello on it"
make -C "$LAB" forge
make -C "$LAB" repo NAME=hello

echo "drill 03: two pods in two namespaces"
reader drill-03-a refuse
reader drill-03-b refuse
for namespace in drill-03-a drill-03-b; do
	kube wait -n "$namespace" --for=condition=Ready pod/reader --timeout="${READY_DEADLINE}s"
done
wait_for_greeting drill-03-a hello
wait_for_greeting drill-03-b hello

echo "drill 03: a push to the forge"
rm -rf "$WORK"
git clone --quiet "$HOST_FORGE" "$WORK"
sed -i 's/^greeting: .*/greeting: goodbye/' "$WORK/greeting.yaml"
git -C "$WORK" add greeting.yaml
git -C "$WORK" -c user.name=lab -c user.email=lab@liken.sh commit --quiet -m "goodbye"
git -C "$WORK" push --quiet origin main

echo "drill 03: the push reaches both pods inside $PULL"
wait_for_greeting drill-03-a goodbye
wait_for_greeting drill-03-b goodbye

echo "drill 03: the forge stops"
make -C "$LAB" forge-stop

echo "drill 03: a stale publish starts, and a refused one does not"
reader drill-03-stale allowStale
reader drill-03-refuse refuse
kube wait -n drill-03-stale --for=condition=Ready pod/reader --timeout="${READY_DEADLINE}s"
wait_for_greeting drill-03-stale goodbye
wait_for_event drill-03-refuse 'GitVolumeRefused|FailedMount'
if kube get -n drill-03-refuse pod/reader -o jsonpath='{.status.phase}' | grep -q Running; then
	echo "drill 03: the refused pod is running, and it must not be" >&2
	exit 1
fi

echo "drill 03: the forge returns"
make -C "$LAB" forge
kube wait -n drill-03-refuse --for=condition=Ready pod/reader --timeout="${READY_DEADLINE}s"
wait_for_greeting drill-03-refuse goodbye

# kubectl top needs a metrics server. A lab without one still passes.
echo "drill 03: the plugin's memory"
kube top pod -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node ||
	echo "drill 03: no metrics server in this lab"

echo "drill 03: passed"
