#!/usr/bin/env bash
# Drill 10. A ReadOnlyMany claim with pull on-demand on the forge's
# repository. A push alone moves nothing. An annotation on the
# PersistentVolume pulls it within seconds, and twenty annotations in a
# burst cost one pull, or two. The driver pod is deleted, and the new
# one pulls once with no annotation. A claim with pull never ignores an
# annotation, and an inline volume with pull on-demand is refused.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

# The forge is on the host, and the guest reaches it at the
# user-mode network's gateway.
GUEST_FORGE="git://10.0.2.2:9418/hello.git"
# The host pushes to the same daemon on its loopback address.
HOST_FORGE="${HOST_FORGE:-git://127.0.0.1:9418/hello.git}"

# The reader image is one the lab already pulled, so no drill waits
# on a registry.
IMAGE="${IMAGE:-debian:12-slim}"
DRIVER_NAMESPACE="${DRIVER_NAMESPACE:-liken-system}"
NAMESPACE=drill-10
# The annotation that demands a pull, which demand.go reads.
DEMAND_ANNOTATION=git.liken.sh/pull-requested-at
# The store on the node keeps a tree per handle, and the forge is
# reseeded on every run, so a handle carries a run's own stamp.
RUN="$(date +%s)"
HANDLE="drill-10-$RUN"
NEVER_HANDLE="drill-10-never-$RUN"
WORK="$LAB/guests/drill-10"

# The five greetings this drill pushes, in order.
FIRST=hello
SECOND=goodbye
THIRD=hello-again
FOURTH=hello-once-more
FIFTH=hello-at-last

# Every wait in this drill has a deadline, in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
REACH_DEADLINE="${REACH_DEADLINE:-180}"
DEMAND_DEADLINE="${DEMAND_DEADLINE:-30}"
EVENT_DEADLINE="${EVENT_DEADLINE:-180}"
HOLD_DEADLINE="${HOLD_DEADLINE:-60}"
BURST_DEADLINE="${BURST_DEADLINE:-5}"
# Longer than --demand-min-interval, so the pull a burst delays has
# run before the counter is read.
SETTLE_SECONDS="${SETTLE_SECONDS:-15}"
BURST=20
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

# cleanup leaves the cluster and the host as the drill found them,
# however the drill ends.
cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$HANDLE" "$NEVER_HANDLE" \
		--ignore-not-found --wait=false >/dev/null 2>&1 || true
	rm -rf "$WORK"
	make -C "$LAB" forge >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT

# fail ends the drill and names the step that did not pass.
fail() {
	local step="$1" text="$2"
	echo "drill 10: step $step failed: $text" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	driver_log | tail -n 40 >&2
	exit 1
}

# driver_log is what the node plugin wrote.
driver_log() {
	kube logs -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node -c driver 2>/dev/null || true
}

# moves counts the lines the driver wrote for this volume when a pull
# placed a new commit in the published tree.
moves() {
	driver_log | grep -cE "the tree moved.*volume=$HANDLE" || true
}

# driver_pod is the node plugin's pod on the one node this lab runs.
driver_pod() {
	kube get pods -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

# demanded_pulls reads git_csi_demanded_pulls_total for this volume.
# The API server proxies the pod's metrics port, so the host reads the
# counter and no pod needs curl. A series the node has not counted yet
# reads as zero.
demanded_pulls() {
	local pod
	pod="$(driver_pod)"
	if [ -z "$pod" ]; then
		echo 0
		return 0
	fi
	{
		kube get --raw \
			"/api/v1/namespaces/$DRIVER_NAMESPACE/pods/$pod:9808/proxy/metrics" 2>/dev/null || true
	} | awk -v ns="namespace=\"$NAMESPACE\"" -v vol="volume=\"$HANDLE\"" '
		/^git_csi_demanded_pulls_total\{/ && index($0, ns) && index($0, vol) { value = $NF }
		END { print value + 0 }'
}

# claim writes a ReadOnlyMany PersistentVolume on the forge's
# repository and the claim that binds it. Neither names a
# VolumeAttributesClass, because a read-only volume takes no policy.
claim() {
	local handle="$1" name="$2" pull="$3"
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
      pull: $pull
      offline: refuse
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

# reader makes one pod that mounts the claim read-only.
reader() {
	local name="$1" claimName="$2"
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
        readOnly: true
YAML
}

# inline_reader makes one pod with an inline volume that names pull
# on-demand. No PersistentVolume carries an inline volume, so nothing
# could demand it.
inline_reader() {
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
          pull: on-demand
          offline: refuse
YAML
}

# greeting is what the pod reads out of the mounted checkout, or
# nothing when the call did not land.
greeting() {
	local name="$1"
	kube exec -n "$NAMESPACE" "$name" -- \
		sh -c 'sed -n "s/^greeting: //p" /hello/greeting.yaml' 2>/dev/null || true
}

# wait_for_greeting waits until the pod reads the greeting, or fails
# at the deadline.
wait_for_greeting() {
	local name="$1" want="$2" seconds="${3:-$REACH_DEADLINE}" deadline
	deadline=$(($(date +%s) + seconds))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if [ "$(greeting "$name")" = "$want" ]; then
			echo "drill 10: $name reads $want"
			return 0
		fi
		sleep 3
	done
	echo "drill 10: $name did not read $want within ${seconds}s" >&2
	return 1
}

# hold_greeting reads the pod for the whole window and fails as soon
# as the greeting changes. An empty read is a call that did not land,
# not a change.
hold_greeting() {
	local name="$1" want="$2" seconds="$3" deadline seen
	deadline=$(($(date +%s) + seconds))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		seen="$(greeting "$name")"
		if [ -n "$seen" ] && [ "$seen" != "$want" ]; then
			echo "drill 10: $name reads $seen, and it has to still read $want" >&2
			return 1
		fi
		sleep 3
	done
	echo "drill 10: $name still reads $want after ${seconds}s"
	return 0
}

# wait_for_event waits until the pod's events carry the pattern, or
# fails at the deadline. It reads one line per event, because the YAML
# emitter folds a long message across lines and a fold splits the
# pattern.
wait_for_event() {
	local name="$1" pattern="$2" deadline
	deadline=$(($(date +%s) + EVENT_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if kube get events -n "$NAMESPACE" \
			--field-selector "involvedObject.kind=Pod,involvedObject.name=$name" \
			-o jsonpath='{range .items[*]}{.reason} {.message}{"\n"}{end}' 2>/dev/null |
			grep -qE "$pattern"; then
			echo "drill 10: $name reported $pattern"
			return 0
		fi
		sleep 3
	done
	echo "drill 10: $name did not report $pattern within ${EVENT_DEADLINE}s" >&2
	return 1
}

# running answers whether the pod reached the Running phase.
running() {
	kube get -n "$NAMESPACE" "pod/$1" -o jsonpath='{.status.phase}' 2>/dev/null |
		grep -q Running
}

# push writes the greeting on the forge and pushes it over the daemon,
# which the lab starts with receive-pack enabled.
push() {
	local greeting="$1"
	rm -rf "$WORK"
	git clone --quiet "$HOST_FORGE" "$WORK"
	sed -i "s/^greeting: .*/greeting: $greeting/" "$WORK/greeting.yaml"
	git -C "$WORK" add greeting.yaml
	git -C "$WORK" -c user.name=lab -c user.email=lab@liken.sh commit --quiet -m "$greeting"
	git -C "$WORK" push --quiet origin main
	echo "drill 10: the forge holds $greeting"
}

# demand writes one annotation the node has not acted on. The value
# carries nanoseconds, so twenty of them are twenty demands.
demand() {
	local handle="$1"
	kube annotate persistentvolume "$handle" \
		"$DEMAND_ANNOTATION=$(date -u +%FT%T.%NZ)" --overwrite >/dev/null
}

echo "drill 10: the node"
kube get nodes

echo "drill 10: the forge, with hello on it"
make -C "$LAB" forge
make -C "$LAB" repo NAME=hello
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true

# Step 1.
echo "drill 10: step 1, a ReadOnlyMany claim with pull on-demand, and a reader pod"
claim "$HANDLE" hello on-demand
reader reader hello
kube wait -n "$NAMESPACE" --for=condition=Ready pod/reader --timeout="${READY_DEADLINE}s" ||
	fail 1 "the reader pod was not Ready within ${READY_DEADLINE}s"
wait_for_greeting reader "$FIRST" ||
	fail 1 "the pod reads the greeting"

# Step 2.
echo "drill 10: step 2, a push, and the pod still reads the old greeting"
push "$SECOND"
hold_greeting reader "$FIRST" "$HOLD_DEADLINE" ||
	fail 2 "the pod still reads the old greeting, because nothing demanded a pull"

# Step 3.
echo "drill 10: step 3, an annotation on the PersistentVolume"
demand "$HANDLE"
wait_for_greeting reader "$SECOND" "$DEMAND_DEADLINE" ||
	fail 3 "the pod reads the new greeting within ${DEMAND_DEADLINE} seconds"

# Step 4. Both counts are taken before and after the burst, so each
# reports what the burst alone added. The counter also counts the
# restart pull, which step 5 makes after this step reads it.
echo "drill 10: step 4, twenty demands inside ${BURST_DEADLINE} seconds"
before_moves="$(moves)"
before_pulls="$(demanded_pulls)"
push "$THIRD"
started="$(date +%s)"
for _ in $(seq 1 "$BURST"); do
	demand "$HANDLE" &
done
wait
burst=$(($(date +%s) - started))
echo "drill 10: $BURST demands took ${burst}s"
if [ "$burst" -gt "$BURST_DEADLINE" ]; then
	fail 4 "the $BURST annotations landed inside $BURST_DEADLINE seconds"
fi
wait_for_greeting reader "$THIRD" ||
	fail 4 "the demanded pull reaches the pod"
sleep "$SETTLE_SECONDS"
moved=$(($(moves) - before_moves))
pulled=$(($(demanded_pulls) - before_pulls))
echo "drill 10: the burst moved the tree $moved times and pulled $pulled times"
if [ "$moved" -ne 1 ]; then
	fail 4 "one pull of the burst moved the tree"
fi
if [ "$pulled" -lt 1 ] || [ "$pulled" -gt 2 ]; then
	fail 4 "the driver counted one pull, or two, inside --demand-min-interval"
fi

# Step 5.
echo "drill 10: step 5, the driver pod is deleted, and the new one pulls with no annotation"
push "$FOURTH"
kube delete pod -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node \
	--timeout="${READY_DEADLINE}s" ||
	fail 5 "the driver pod on the node is deleted"
kube rollout status -n "$DRIVER_NAMESPACE" daemonset/git-csi-driver-node \
	--timeout="${READY_DEADLINE}s" ||
	fail 5 "the new driver pod is ready"
wait_for_greeting reader "$FOURTH" ||
	fail 5 "the pod reads the third greeting without an annotation"

# Step 6.
echo "drill 10: step 6, a second claim with pull never on the same repository"
claim "$NEVER_HANDLE" never never
reader reader-never never
kube wait -n "$NAMESPACE" --for=condition=Ready pod/reader-never \
	--timeout="${READY_DEADLINE}s" ||
	fail 6 "the second reader pod was not Ready within ${READY_DEADLINE}s"
wait_for_greeting reader-never "$FOURTH" ||
	fail 6 "the second pod reads what it staged"
push "$FIFTH"
demand "$NEVER_HANDLE"
hold_greeting reader-never "$FOURTH" "$HOLD_DEADLINE" ||
	fail 6 "its pod never reads the new greeting"

# Step 7.
echo "drill 10: step 7, an inline volume with pull on-demand is refused"
inline_reader inline
wait_for_event inline 'an inline volume has none' ||
	fail 7 "the stage is refused, and the pod's Event says why"
if running inline; then
	fail 7 "the refused pod is not running"
fi

echo "drill 10: passed"
