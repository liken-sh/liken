package main

import "testing"

// runPod returns a playback pod with the given UID, with a deletion
// timestamp when deleting is true.
func runPod(uid string, deleting bool) *Pod {
	pod := &Pod{Metadata: ObjectMeta{Name: "movie-playback", Namespace: "house", UID: uid}}
	if deleting {
		pod.Metadata.DeletionTimestamp = "2026-09-09T10:30:00Z"
	}
	return pod
}

// podsWithStore returns a runPods whose pod store has pod, or no pod
// when pod is nil.
func podsWithStore(pod *Pod) runPods {
	pods := newRunPods()
	pods.lookup = func(string, string) *Pod { return pod }
	return pods
}

func TestAMessageCountsOnlyFromTheRunsPod(t *testing.T) {
	cases := []struct {
		name  string
		store *Pod
		gone  string
		uid   string
		want  bool
	}{
		{name: "a message with no UID", store: runPod("new", false), uid: "", want: true},
		{name: "the pod the store has", store: runPod("new", false), uid: "new", want: true},
		{name: "another pod while the store has a current one", store: runPod("new", false), uid: "old", want: false},
		{name: "the store's pod while it is being deleted", store: runPod("old", true), uid: "old", want: false},
		{name: "a new pod while the store's pod is being deleted", store: runPod("old", true), uid: "new", want: true},
		{name: "a pod not recorded as gone while the store has none", uid: "new", want: true},
		{name: "a pod recorded as gone while the store has none", gone: "old", uid: "old", want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			pods := podsWithStore(each.store)
			pods.markGone(runKey("house", "movie"), each.gone)

			mustMatch(t, pods.fromRunPod("house", "movie", each.uid), each.want)
		})
	}
}

func TestAMessageCountsBeforeThePodStoreIsRead(t *testing.T) {
	pods := newRunPods()

	mustMatch(t, pods.fromRunPod("house", "movie", "new"), true)
}

// Once the store no longer shows a pod as current, a late message from
// that pod is rejected, even when the store has no pod for the run.
func TestAPodTheStoreNoLongerHoldsIsRecordedAsGone(t *testing.T) {
	cases := []struct {
		name  string
		later *Pod
	}{
		{name: "the store has another pod", later: runPod("new", false)},
		{name: "the store's pod is being deleted", later: runPod("old", true)},
		{name: "the store has no pod"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			pods := podsWithStore(nil)
			key := runKey("house", "movie")
			pods.observe(key, runPod("old", false))
			pods.observe(key, each.later)

			mustMatch(t, pods.fromRunPod("house", "movie", "old"), false)
		})
	}
}

// retain drops the records of a Play the pass no longer lists, so a Play
// created later under the same name starts with no pod recorded as gone.
func TestRetainDropsTheRecordOfAPlayThatIsGone(t *testing.T) {
	pods := podsWithStore(nil)
	key := runKey("house", "movie")
	pods.observe(key, runPod("old", false))
	pods.markGone(key, "old")

	pods.retain(map[string]bool{})

	mustMatch(t, pods.fromRunPod("house", "movie", "old"), true)
}
