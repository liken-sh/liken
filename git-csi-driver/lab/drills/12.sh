#!/usr/bin/env bash
# Drill 12: webhooks. The drill creates a webhook Secret in its own
# namespace and a ReadOnlyMany claim with pull on-demand that names it,
# then checks that:
#  - a signed GitHub-style push request marks the volume for a pull,
#    and the pod reads the new greeting within seconds.
#  - a wrong signature, a path that names no Secret, and a repository
#    that no volume follows get the documented responses and change
#    nothing.
#  - twenty signed requests at once cause one pull, or two.
#  - a new controller pod marks every volume when it starts.
#  - a Gitea-style request is accepted too.
# The lab's forge is git daemon, which sends no webhooks, so the drill
# signs and posts every request itself.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

# The forge runs on the host. The guest reaches it at the user-mode
# network's gateway.
GUEST_FORGE="git://10.0.2.2:9418/hello.git"
# The host pushes to the same daemon through its loopback address.
HOST_FORGE="${HOST_FORGE:-git://127.0.0.1:9418/hello.git}"
# A repository on the same forge that no volume follows.
OTHER_FORGE="git://10.0.2.2:9418/other.git"

# The reader and poster pods use this image, which the lab already has.
IMAGE="${IMAGE:-debian:sid-slim}"
DRIVER_NAMESPACE="${DRIVER_NAMESPACE:-liken-system}"
NAMESPACE=drill-12
# The Service for the controller's webhook listener, on port 80.
WEBHOOK_HOST="git-csi-driver-webhook.$DRIVER_NAMESPACE.svc"
# The Secret that the webhook path names, and the HMAC key in it.
SECRET_NAME=drill-12-webhook
SECRET="$(openssl rand -hex 16)"
# The node's store keeps one tree for each volume handle, and each run
# recreates the forge, so the handle has a timestamp to make it unique
# to the run.
RUN="$(date +%s)"
HANDLE="drill-12-$RUN"
WORK="$LAB/guests/drill-12"
PAYLOADS="$LAB/guests/drill-12-payloads"
POSTER=poster

# The greetings that the drill pushes, in order. UNSEEN is on the forge
# while the drill posts the requests that must change nothing, so a
# request that the listener wrongly accepts changes the tree to it.
FIRST=hello
SECOND=goodbye
UNSEEN=not-for-you
THIRD=hello-again
FOURTH=hello-once-more

# The deadlines of the waits in this drill, in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
REACH_DEADLINE="${REACH_DEADLINE:-180}"
WEBHOOK_DEADLINE="${WEBHOOK_DEADLINE:-30}"
HOLD_DEADLINE="${HOLD_DEADLINE:-20}"
# The burst deadline equals the default --demand-min-interval. Posts
# that take longer than one interval can cause more pulls, so the step
# fails on the time before it fails on the pull count.
BURST_DEADLINE="${BURST_DEADLINE:-10}"
# Longer than --demand-min-interval, so that the pull that the burst
# delays has run before the drill reads the counter.
SETTLE_SECONDS="${SETTLE_SECONDS:-15}"
BURST=20
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

# cleanup runs on every exit, including a failed step.
cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$HANDLE" \
		--ignore-not-found --wait=false >/dev/null 2>&1 || true
	rm -rf "$WORK" "$PAYLOADS"
	make -C "$LAB" forge >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT

fail() {
	local step="$1" text="$2"
	echo "drill 12: step $step failed: $text" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	controller_log | tail -n 40 >&2
	driver_log | tail -n 40 >&2
	exit 1
}

driver_log() {
	kube logs -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node -c driver 2>/dev/null || true
}

controller_log() {
	kube logs -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-controller -c driver 2>/dev/null || true
}

driver_pod() {
	kube get pods -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

# demanded_pulls reads git_csi_demanded_pulls_total for this volume
# through the API server's pod proxy, so no pod needs curl. A series
# that does not exist yet counts as zero.
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

webhook_secret() {
	kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Secret
metadata:
  name: $SECRET_NAME
  namespace: $NAMESPACE
stringData:
  secret: $SECRET
YAML
}

# claim creates a ReadOnlyMany PersistentVolume with pull on-demand and
# the webhookSecret attribute, and its PersistentVolumeClaim in the
# Secret's namespace.
claim() {
	local handle="$1" name="$2"
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
      pull: on-demand
      offline: refuse
      webhookSecret: $SECRET_NAME
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

# greeting prints the greeting in the pod's checkout, or nothing when
# the exec call fails.
greeting() {
	local name="$1"
	kube exec -n "$NAMESPACE" "$name" -- \
		sh -c 'sed -n "s/^greeting: //p" /hello/greeting.yaml' 2>/dev/null || true
}

wait_for_greeting() {
	local name="$1" want="$2" seconds="${3:-$REACH_DEADLINE}" deadline
	deadline=$(($(date +%s) + seconds))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if [ "$(greeting "$name")" = "$want" ]; then
			echo "drill 12: $name reads $want"
			return 0
		fi
		sleep 3
	done
	echo "drill 12: $name did not read $want within ${seconds}s" >&2
	return 1
}

# hold_greeting fails as soon as the greeting changes within the
# window. An empty read means that the exec call failed, so it does not
# count as a change.
hold_greeting() {
	local name="$1" want="$2" seconds="$3" deadline seen
	deadline=$(($(date +%s) + seconds))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		seen="$(greeting "$name")"
		if [ -n "$seen" ] && [ "$seen" != "$want" ]; then
			echo "drill 12: $name reads $seen, and it has to still read $want" >&2
			return 1
		fi
		sleep 3
	done
	echo "drill 12: $name still reads $want after ${seconds}s"
	return 0
}

push() {
	local greeting="$1"
	rm -rf "$WORK"
	git clone --quiet "$HOST_FORGE" "$WORK"
	sed -i "s/^greeting: .*/greeting: $greeting/" "$WORK/greeting.yaml"
	git -C "$WORK" add greeting.yaml
	git -C "$WORK" -c user.name=lab -c user.email=lab@liken.sh commit --quiet -m "$greeting"
	git -C "$WORK" push --quiet origin main
	echo "drill 12: the forge has $greeting"
}

# payload writes a GitHub-style push body with no trailing newline, so
# that the signed bytes equal the posted bytes.
payload() {
	local file="$1" url="$2"
	printf '%s' \
		"{\"ref\":\"refs/heads/main\",\"repository\":{\"clone_url\":\"$url\",\"ssh_url\":\"\",\"html_url\":\"\"}}" \
		>"$file"
}

signature() {
	local key="$1" file="$2"
	openssl dgst -sha256 -hmac "$key" "$file" | awk '{print $NF}'
}

# poster_script runs inside the poster pod. The image has no curl, so
# the script sends a raw HTTP/1.1 request through bash's /dev/tcp.
poster_script="$(
	cat <<'SCRIPT'
set -eu
host="$1"; path="$2"; header="$3"; value="$4"; body="$5"
exec 3<>"/dev/tcp/$host/80"
printf "POST %s HTTP/1.1\r\nHost: %s\r\n%s: %s\r\nContent-Type: application/json\r\nContent-Length: %s\r\nConnection: close\r\n\r\n%s" \
	"$path" "$host" "$header" "$value" "${#body}" "$body" >&3
cat <&3
SCRIPT
)"

# post prints the status code on the first line and the response body
# after it.
post() {
	local path="$1" header="$2" value="$3" file="$4" body
	body="$(cat "$file")"
	kube exec -n "$NAMESPACE" "$POSTER" -- \
		bash -c "$poster_script" post "$WEBHOOK_HOST" "$path" "$header" "$value" "$body" |
		awk '
			NR == 1 { sub(/\r$/, ""); split($0, line, " "); print line[2]; next }
			body { sub(/\r$/, ""); print; next }
			/^\r?$/ { body = 1 }'
}

status_code() {
	printf '%s\n' "$1" | head -n 1
}

body() {
	printf '%s\n' "$1" | tail -n +2
}

echo "drill 12: the node"
kube get nodes

echo "drill 12: the forge, with hello on it"
make -C "$LAB" forge
make -C "$LAB" repo NAME=hello
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
mkdir -p "$PAYLOADS"
payload "$PAYLOADS/hello.json" "$GUEST_FORGE"
payload "$PAYLOADS/other.json" "$OTHER_FORGE"
HELLO_SIGNATURE="$(signature "$SECRET" "$PAYLOADS/hello.json")"
OTHER_SIGNATURE="$(signature "$SECRET" "$PAYLOADS/other.json")"
WRONG_SIGNATURE="$(signature "not-$SECRET" "$PAYLOADS/hello.json")"
WEBHOOK_PATH="/webhook/$NAMESPACE/$SECRET_NAME"

# The webhook Service has only a cluster address, so a pod posts every
# request.
kube run "$POSTER" -n "$NAMESPACE" --image="$IMAGE" --restart=Never \
	--overrides='{"spec":{"tolerations":[{"operator":"Exists"}]}}' \
	--command -- sleep infinity >/dev/null
kube wait -n "$NAMESPACE" --for=condition=Ready "pod/$POSTER" \
	--timeout="${READY_DEADLINE}s" ||
	fail 1 "the poster pod was not Ready within ${READY_DEADLINE}s"

echo "drill 12: step 1, a webhook Secret, a claim with pull on-demand, and a reader pod"
webhook_secret
claim "$HANDLE" hello
reader reader hello
kube wait -n "$NAMESPACE" --for=condition=Ready pod/reader --timeout="${READY_DEADLINE}s" ||
	fail 1 "the reader pod was not Ready within ${READY_DEADLINE}s"
wait_for_greeting reader "$FIRST" ||
	fail 1 "the pod reads the greeting"

echo "drill 12: step 2, a push, and a signed GitHub payload that names one volume"
push "$SECOND"
response="$(post "$WEBHOOK_PATH" X-Hub-Signature-256 "sha256=$HELLO_SIGNATURE" \
	"$PAYLOADS/hello.json")" || fail 2 "the request reached the Service"
echo "drill 12: the listener responded $(status_code "$response") $(body "$response")"
[ "$(status_code "$response")" = "202" ] ||
	fail 2 "the response is 202"
[ "$(body "$response")" = "marked 1" ] ||
	fail 2 "the response counts one marked volume"
wait_for_greeting reader "$SECOND" "$WEBHOOK_DEADLINE" ||
	fail 2 "the pod reads the new greeting within ${WEBHOOK_DEADLINE} seconds"

echo "drill 12: step 3, a push, and the same payload with a wrong signature"
push "$UNSEEN"
response="$(post "$WEBHOOK_PATH" X-Hub-Signature-256 "sha256=$WRONG_SIGNATURE" \
	"$PAYLOADS/hello.json")" || fail 3 "the request reached the Service"
echo "drill 12: the listener responded $(status_code "$response")"
[ "$(status_code "$response")" = "401" ] ||
	fail 3 "the response is 401"
hold_greeting reader "$SECOND" "$HOLD_DEADLINE" ||
	fail 3 "the tree does not move"

echo "drill 12: step 4, the same payload to a path that names no Secret"
response="$(post "/webhook/$NAMESPACE/no-such-secret" X-Hub-Signature-256 \
	"sha256=$HELLO_SIGNATURE" "$PAYLOADS/hello.json")" ||
	fail 4 "the request reached the Service"
echo "drill 12: the listener responded $(status_code "$response")"
[ "$(status_code "$response")" = "401" ] ||
	fail 4 "the response is 401"

echo "drill 12: step 5, a signed payload naming a repository no volume follows"
response="$(post "$WEBHOOK_PATH" X-Hub-Signature-256 "sha256=$OTHER_SIGNATURE" \
	"$PAYLOADS/other.json")" || fail 5 "the request reached the Service"
echo "drill 12: the listener responded $(status_code "$response") $(body "$response")"
[ "$(status_code "$response")" = "202" ] ||
	fail 5 "the response is 202"
[ "$(body "$response")" = "marked 0" ] ||
	fail 5 "the response counts zero marked volumes"
hold_greeting reader "$SECOND" "$HOLD_DEADLINE" ||
	fail 5 "the tree does not move"

echo "drill 12: step 6, twenty signed payloads at once"
before_pulls="$(demanded_pulls)"
push "$THIRD"
started="$(date +%s)"
for _ in $(seq 1 "$BURST"); do
	post "$WEBHOOK_PATH" X-Hub-Signature-256 "sha256=$HELLO_SIGNATURE" \
		"$PAYLOADS/hello.json" >/dev/null 2>&1 &
done
wait
burst=$(($(date +%s) - started))
echo "drill 12: $BURST posts ran for ${burst}s"
if [ "$burst" -gt "$BURST_DEADLINE" ]; then
	fail 6 "the $BURST posts were accepted within $BURST_DEADLINE seconds"
fi
wait_for_greeting reader "$THIRD" ||
	fail 6 "the demanded pull reaches the pod"
sleep "$SETTLE_SECONDS"
pulled=$(($(demanded_pulls) - before_pulls))
echo "drill 12: the burst pulled $pulled times"
if [ "$pulled" -lt 1 ] || [ "$pulled" -gt 2 ]; then
	fail 6 "the driver counted one pull, or two, inside --demand-min-interval"
fi

echo "drill 12: step 7, the controller pod is deleted, and the new one marks every volume"
push "$FOURTH"
kube delete pod -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-controller \
	--timeout="${READY_DEADLINE}s" ||
	fail 7 "the controller pod is deleted"
kube rollout status -n "$DRIVER_NAMESPACE" deployment/git-csi-driver-controller \
	--timeout="${READY_DEADLINE}s" ||
	fail 7 "the new controller pod is ready"
wait_for_greeting reader "$FOURTH" ||
	fail 7 "the pod reads the fourth greeting without a webhook"

echo "drill 12: step 8, a Gitea-shaped payload with X-Gitea-Signature"
response="$(post "$WEBHOOK_PATH" X-Gitea-Signature "$HELLO_SIGNATURE" \
	"$PAYLOADS/hello.json")" || fail 8 "the request reached the Service"
echo "drill 12: the listener responded $(status_code "$response") $(body "$response")"
[ "$(status_code "$response")" = "202" ] ||
	fail 8 "the response is 202"

echo "drill 12: passed"
