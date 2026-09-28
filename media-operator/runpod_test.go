package main

import "testing"

// runPod is a playback pod with a UID, deleting or standing.
func runPod(uid string, deleting bool) *Pod {
	pod := &Pod{Metadata: ObjectMeta{Name: "movie-playback", Namespace: "house", UID: uid}}
	if deleting {
		pod.Metadata.DeletionTimestamp = "2026-09-09T10:30:00Z"
	}
	return pod
}

// podsWithStore is a record whose store holds the one pod given, or
// none for nil.
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
		{name: "the standing pod the store holds", store: runPod("new", false), uid: "new", want: true},
		{name: "another pod while the store holds a standing one", store: runPod("new", false), uid: "old", want: false},
		{name: "the pod the store holds deleting", store: runPod("old", true), uid: "old", want: false},
		{name: "a new pod while the store holds the old one deleting", store: runPod("old", true), uid: "new", want: true},
		{name: "a pod not known gone while the store holds none", uid: "new", want: true},
		{name: "a pod known gone while the store holds none", gone: "old", uid: "old", want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			pods := podsWithStore(each.store)
			pods.markGone(runKey("house", "movie"), each.gone)

			mustMatch(t, pods.fromRunPod("house", "movie", each.uid), each.want)
		})
	}
}

// Before the watches have read the cluster the desk has no store, and a
// pod not known gone counts.
func TestAMessageCountsBeforeTheStoreIsRead(t *testing.T) {
	pods := newRunPods()

	mustMatch(t, pods.fromRunPod("house", "movie", "new"), true)
}

// A pod the store held stops counting once the store holds another pod,
// or none, so a late message from it counts for nothing even while the
// store holds no pod at all.
func TestAPodTheStoreNoLongerHoldsIsGone(t *testing.T) {
	cases := []struct {
		name  string
		later *Pod
	}{
		{name: "the store holds another pod", later: runPod("new", false)},
		{name: "the store holds the pod deleting", later: runPod("old", true)},
		{name: "the store holds no pod"},
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

// The record of a Play the pass no longer lists goes, so a Play created
// later under the same name starts with nothing known gone.
func TestTheRecordOfAPlayThatIsGoneGoes(t *testing.T) {
	pods := podsWithStore(nil)
	key := runKey("house", "movie")
	pods.observe(key, runPod("old", false))
	pods.markGone(key, "old")

	pods.retain(map[string]bool{})

	mustMatch(t, pods.fromRunPod("house", "movie", "old"), true)
}
