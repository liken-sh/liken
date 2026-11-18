#!/usr/bin/env bash
# Drill 08. Two writers on one repository against the host forge. Each
# pod mounts its own directory of the same ref through subPath and
# writes in it. Both changes reach main with no side branch, and one
# claim reports GitVolumeRebased. Then both pods write the same file in
# a shared directory, and one volume pushes to its side branch. After a
# person merges that branch on the forge, the pod's next write puts the
# volume back on main while the pod keeps running.
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
KUBECTL="$LAB/kubectl"

# The forge is a git daemon on the host. 10.0.2.2 is the host as a QEMU
# guest sees it on user-mode networking.
GUEST_FORGE="git://10.0.2.2:9418/hello.git"
FORGE="$LAB/forge/hello.git"

# The writer image is already in the lab's image store, so the drill
# does not wait on a registry.
IMAGE="${IMAGE:-debian:sid-slim}"
DRIVER_NAMESPACE="${DRIVER_NAMESPACE:-liken-system}"
NAMESPACE=drill-08
# The node's store keeps a work tree for each volume handle, and
# `make repo` creates the forge's repository again on every run. A
# handle from an earlier run would find that run's tree and push commits
# the new repository does not have, so each handle includes the run's
# start time.
RUN="$(date +%s)"
HANDLE_A="drill-08-assistant-$RUN"
HANDLE_B="drill-08-maps-$RUN"
CLASS=drill-08

# Every wait in this drill gives up after a deadline in seconds.
READY_DEADLINE="${READY_DEADLINE:-240}"
ARMED_DEADLINE="${ARMED_DEADLINE:-180}"
PUSH_DEADLINE="${PUSH_DEADLINE:-180}"
EVENT_DEADLINE="${EVENT_DEADLINE:-180}"
SIDE_DEADLINE="${SIDE_DEADLINE:-240}"
HEAL_DEADLINE="${HEAL_DEADLINE:-240}"
CALL_TIMEOUT="${CALL_TIMEOUT:-30s}"

kube() {
	"$KUBECTL" --request-timeout="$CALL_TIMEOUT" "$@"
}

cleanup() {
	local status=$?
	kube delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete persistentvolume "$HANDLE_A" "$HANDLE_B" \
		--ignore-not-found --wait=false >/dev/null 2>&1 || true
	kube delete volumeattributesclass "$CLASS" --ignore-not-found >/dev/null 2>&1 || true
	rm -rf "$LAB/guests/drill-08-clone"
	return "$status"
}
trap cleanup EXIT

driver_log() {
	kube logs -n "$DRIVER_NAMESPACE" -l app=git-csi-driver-node -c driver --tail=400 2>/dev/null || true
}

wait_for_event() {
	local pattern="$1" deadline
	deadline=$(($(date +%s) + EVENT_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if kube get events -n "$NAMESPACE" -o yaml 2>/dev/null | grep -qE "$pattern"; then
			echo "drill 08: the events report $pattern"
			return 0
		fi
		sleep 3
	done
	echo "drill 08: no event matched $pattern within ${EVENT_DEADLINE}s" >&2
	kube get events -n "$NAMESPACE" --sort-by=.lastTimestamp >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

# wait_for_content waits until the file on the forge's branch has a line
# equal to the text, which shows that the push arrived.
wait_for_content() {
	local branch="$1" path="$2" want="$3" deadline="$4"
	local limit
	limit=$(($(date +%s) + deadline))
	while [ "$(date +%s)" -lt "$limit" ]; do
		if git -C "$FORGE" show "$branch:$path" 2>/dev/null | grep -qxF "$want"; then
			echo "drill 08: $branch has $want in $path"
			return 0
		fi
		sleep 3
	done
	echo "drill 08: $branch did not hold $want in $path within ${deadline}s" >&2
	git -C "$FORGE" log --oneline -5 --all >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

on_forge() {
	local branch="$1"
	git -C "$FORGE" for-each-ref --format='%(refname:short)' refs/heads/ |
		grep -qxF "$branch"
}

# wait_for_branch waits until the branch is on the forge when the second
# argument is "there", or until it is not when the argument is "gone".
wait_for_branch() {
	local branch="$1" state="$2" deadline="$3"
	local limit found
	limit=$(($(date +%s) + deadline))
	while [ "$(date +%s)" -lt "$limit" ]; do
		found=no
		if on_forge "$branch"; then
			found=yes
		fi
		if { [ "$state" = "gone" ] && [ "$found" = "no" ]; } ||
			{ [ "$state" = "there" ] && [ "$found" = "yes" ]; }; then
			echo "drill 08: the forge has $branch: $found"
			return 0
		fi
		sleep 3
	done
	echo "drill 08: $branch was not $state within ${deadline}s" >&2
	git -C "$FORGE" for-each-ref --format='%(refname:short)' refs/heads/ >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

# side_branches prints how many of the two volumes have a side branch on
# the forge.
side_branches() {
	local count=0 handle
	for handle in "$HANDLE_A" "$HANDLE_B"; do
		if on_forge "main.$handle"; then
			count=$((count + 1))
		fi
	done
	echo "$count"
}

wait_for_one_side_branch() {
	local deadline
	deadline=$(($(date +%s) + SIDE_DEADLINE))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if [ "$(side_branches)" = "1" ]; then
			echo "drill 08: one of the two volumes diverged to its side branch"
			return 0
		fi
		sleep 3
	done
	echo "drill 08: $(side_branches) volumes diverged to a side branch within ${SIDE_DEADLINE}s, want 1" >&2
	git -C "$FORGE" for-each-ref --format='%(refname:short)' refs/heads/ >&2 || true
	driver_log | tail -n 40 >&2
	return 1
}

# diverged_handle prints the handle of the volume that has a side branch.
diverged_handle() {
	if on_forge "main.$HANDLE_A"; then
		echo "$HANDLE_A"
		return 0
	fi
	echo "$HANDLE_B"
}

writer_of() {
	if [ "$1" = "$HANDLE_A" ]; then
		echo writer-a
		return 0
	fi
	echo writer-b
}

# merge_side merges the side branch into main and pushes main, as a
# person would. On a conflict it keeps the side branch's shared/one.yaml.
merge_side() {
	local branch="$1"
	rm -rf "$LAB/guests/drill-08-clone"
	git clone --quiet "$FORGE" "$LAB/guests/drill-08-clone"
	(
		cd "$LAB/guests/drill-08-clone"
		git -c user.name=lab -c user.email=lab@liken.sh \
			merge --quiet --no-ff -m "merge $branch" "origin/$branch" || {
			git checkout --theirs shared/one.yaml
			git add shared/one.yaml
			git -c user.name=lab -c user.email=lab@liken.sh \
				commit --quiet -m "merge $branch"
		}
		git push --quiet origin main
	)
	git -C "$FORGE" log --oneline -3 main
}

# seed commits one directory for each writer and a shared directory to
# main, like a repository that holds the configuration of many
# workloads.
seed() {
	rm -rf "$LAB/guests/drill-08-clone"
	git clone --quiet "$FORGE" "$LAB/guests/drill-08-clone"
	(
		cd "$LAB/guests/drill-08-clone"
		mkdir -p assistant maps shared
		echo forge-assistant >assistant/settings.yaml
		echo forge-maps >maps/settings.yaml
		echo forge-shared >shared/settings.yaml
		git add assistant maps shared
		git -c user.name=lab -c user.email=lab@liken.sh \
			commit --quiet -m "the two writers' directories"
		git push --quiet origin main
	)
	git -C "$FORGE" ls-tree -r --name-only main
}

# class creates the VolumeAttributesClass that both claims name. Its
# push.quiesce is short, so a commit happens within seconds.
class() {
	kube apply -f - >/dev/null <<YAML
apiVersion: storage.k8s.io/v1
kind: VolumeAttributesClass
metadata:
  name: $CLASS
driverName: git.liken.sh
parameters:
  push.quiesce: 10s
  push.maxLatency: 5m
  commit.maxFileSize: 1Mi
  commit.author: The lab <lab@liken.sh>
YAML
}

# volume creates a writeable PersistentVolume and the claim that binds
# it. The drill calls it once for each writer, on the same repository.
volume() {
	local handle="$1" name="$2"
	kube apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: $handle
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOncePod]
  persistentVolumeReclaimPolicy: Retain
  # The binder binds a claim to a volume only when both set the same
  # volumeAttributesClassName.
  volumeAttributesClassName: $CLASS
  csi:
    driver: git.liken.sh
    volumeHandle: $handle
    volumeAttributes:
      url: $GUEST_FORGE
      ref: main
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $name
  namespace: $NAMESPACE
spec:
  volumeName: $handle
  volumeAttributesClassName: $CLASS
  accessModes: [ReadWriteOncePod]
  resources: {requests: {storage: 1Gi}}
  storageClassName: ""
YAML
}

# writer runs a pod that mounts the claim twice with subPath: the pod's
# own directory of the repository at /config and the shared directory at
# /shared.
writer() {
	local name="$1" claimName="$2" directory="$3"
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
    - name: writer
      image: $IMAGE
      command: ["sh", "-c", "sleep infinity"]
      volumeMounts:
        - name: config
          mountPath: /config
          subPath: $directory
        - name: config
          mountPath: /shared
          subPath: shared
  volumes:
    - name: config
      persistentVolumeClaim:
        claimName: $claimName
YAML
	kube wait -n "$NAMESPACE" --for=condition=Ready "pod/$name" --timeout="${READY_DEADLINE}s"
}

echo "drill 08: the node"
kube get nodes

echo "drill 08: the forge, with hello on it"
make -C "$LAB" forge
make -C "$LAB" repo NAME=hello
seed

echo "drill 08: the class, and one volume and claim for each writer"
kube create namespace "$NAMESPACE" >/dev/null 2>&1 || true
class
volume "$HANDLE_A" assistant
volume "$HANDLE_B" maps

echo "drill 08: two pods, each holding one directory of the repository"
writer writer-a assistant assistant
writer writer-b maps maps

echo "drill 08: each pod writes in its own directory"
kube exec -n "$NAMESPACE" writer-a -- sh -c 'echo pod-a > /config/one.yaml'
kube exec -n "$NAMESPACE" writer-b -- sh -c 'echo pod-b > /config/one.yaml'

echo "drill 08: both writers' work reaches main"
wait_for_content main assistant/one.yaml pod-a "$PUSH_DEADLINE"
wait_for_content main maps/one.yaml pod-b "$PUSH_DEADLINE"

echo "drill 08: the writer whose push was rejected rebased and pushed to main"
wait_for_event "GitVolumeRebased"

echo "drill 08: neither volume diverged to a side branch"
wait_for_branch "main.$HANDLE_A" gone 1
wait_for_branch "main.$HANDLE_B" gone 1

echo "drill 08: both pods write the same file in the directory they share"
kube exec -n "$NAMESPACE" writer-a -- sh -c 'echo pod-a > /shared/one.yaml'
kube exec -n "$NAMESPACE" writer-b -- sh -c 'echo pod-b > /shared/one.yaml'

echo "drill 08: one of the two volumes diverges to its side branch"
wait_for_one_side_branch
wait_for_event "GitVolumeDiverged"
git -C "$FORGE" for-each-ref --format='%(refname:short)' refs/heads/
driver_log | grep -iE "rebased|diverged" | tail -n 10

echo "drill 08: a person merges the side branch on the forge"
DIVERGED="$(diverged_handle)"
POD="$(writer_of "$DIVERGED")"
echo "drill 08: $POD lost the write, and main.$DIVERGED is on the forge"
merge_side "main.$DIVERGED"

echo "drill 08: the volume returns to main at its next push, with no restart"
kube exec -n "$NAMESPACE" "$POD" -- sh -c 'echo pod-healed > /shared/two.yaml'
wait_for_content main shared/two.yaml pod-healed "$HEAL_DEADLINE"
wait_for_branch "main.$DIVERGED" gone "$HEAL_DEADLINE"
wait_for_event "GitVolumeHealed"
driver_log | grep -E "returned to its ref" | tail -n 5

echo "drill 08: passed"
