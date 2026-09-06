package main

// What these tests read: the owner references that tie a Watch to its
// people, and the projection the store publishes reaching the Watch's
// status.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A Watch as a person writes it, in the namespace the seeded house
// keeps its library in.
func seedWatch(cluster *fakeCluster, name string, people ...string) *Watch {
	watch := &Watch{
		Metadata: ObjectMeta{
			Name: name, Namespace: testLibraryNamespace,
			UID: name + "-uid", ResourceVersion: "1",
		},
		Spec: WatchSpec{People: people, Item: WatchItem{Library: "series", Slug: "the-office"}},
	}
	cluster.watches[name] = watch
	return watch
}

// One Watch's projection out of the store, delivered the way the broker
// delivers it.
func publishProgress(operator *operator, name string, progress watchProgress) {
	payload, err := json.Marshal(progress)
	if err != nil {
		panic(err)
	}
	operator.handleBusMessage(
		watchProgressTopic(defaultTopicBase, testLibraryNamespace, name), payload)
}

// A Watch goes when the last of its people goes, and the owner
// references are the whole of that rule.
func TestAWatchIsOwnedByThePeopleItNames(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedPerson(cluster, "chris")
	seedPerson(cluster, "thora")
	seedWatch(cluster, "the-girls", "thora", "chris")

	operator.pass()

	owners := cluster.heldWatch("the-girls").Metadata.OwnerReferences
	if len(owners) != 2 {
		t.Fatalf("owners = %+v, want one per person", owners)
	}
	if owners[0].Name != "chris" || owners[1].Name != "thora" {
		t.Errorf("owners = %+v, want chris and thora in name order", owners)
	}
	if owners[0].APIVersion != personAPIVersion || owners[0].Kind != personKind {
		t.Errorf("owner = %+v, want a Person of the people group", owners[0])
	}
	if owners[0].UID != "chris-uid" || owners[0].Controller {
		t.Errorf("owner = %+v, want the person's uid and no controller flag", owners[0])
	}
}

// A name that names nobody is dropped, because a reference to an object
// that does not exist would have the garbage collector delete the
// Watch.
func TestAWatchIsNotOwnedByANameNobodyHolds(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedPerson(cluster, "chris")
	seedWatch(cluster, "the-girls", "chris", "nobody")

	operator.pass()

	owners := cluster.heldWatch("the-girls").Metadata.OwnerReferences
	if len(owners) != 1 || owners[0].Name != "chris" {
		t.Errorf("owners = %+v, want the one person the cluster holds", owners)
	}
}

// An owner of another kind is left as it stands, because something else
// put it there.
func TestAWatchKeepsTheOwnersOfOtherKinds(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedPerson(cluster, "chris")
	watch := seedWatch(cluster, "the-girls", "chris")
	watch.Metadata.OwnerReferences = []OwnerReference{
		{APIVersion: libraryAPIVersion, Kind: "Library", Name: "series", UID: "series-uid"},
	}

	operator.pass()

	owners := cluster.heldWatch("the-girls").Metadata.OwnerReferences
	if len(owners) != 2 || owners[0].Kind != "Library" || owners[1].Name != "chris" {
		t.Errorf("owners = %+v, want the library kept and the person added", owners)
	}
}

// The status is a projection of the store, and the operator rewrites it
// as rows arrive.
func TestTheProjectionReachesTheWatchStatus(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedWatch(cluster, "the-girls", "chris")
	publishProgress(operator, "the-girls", watchProgress{
		Play: "den-tv-the-office-b2k9x", Item: 1, Position: "0:12:00", Duration: "0:22:00",
		Season: 3, Episode: 5, Ended: false, LastRecorded: "2026-09-06T21:14:02Z",
	})

	operator.pass()

	status := cluster.heldWatch("the-girls").Status
	want := WatchStatus{
		Play: "den-tv-the-office-b2k9x", Item: 1, Position: "0:12:00", Duration: "0:22:00",
		Season: 3, Episode: 5, LastRecorded: "2026-09-06T21:14:02Z",
	}
	if status != want {
		t.Errorf("status = %+v, want %+v", status, want)
	}
}

// A pass writes the status only where the projection changed, so a
// steady pass costs the API server nothing.
func TestAnUnchangedProjectionIsNotWrittenAgain(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedWatch(cluster, "the-girls", "chris")
	publishProgress(operator, "the-girls", watchProgress{
		Play: "den-tv-the-office-b2k9x", Item: 1, Position: "0:12:00",
		LastRecorded: "2026-09-06T21:14:02Z",
	})

	operator.pass()
	operator.pass()

	if writes := cluster.countRequests(http.MethodPut, "watches"); writes != 1 {
		t.Errorf("the passes wrote the status %d times, want one write", writes)
	}
}

// A Watch the store has published nothing for keeps the status it
// carries.
func TestAWatchWithNoProjectionIsNotWritten(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedWatch(cluster, "the-girls", "chris")

	operator.pass()

	if writes := cluster.countRequests(http.MethodPut, "watches"); writes != 0 {
		t.Errorf("the pass wrote the status %d times, want none", writes)
	}
}

// A pass reads every Watch in the cluster with one request, the way it
// reads the Libraries.
func TestListWatchesReadsEveryNamespace(t *testing.T) {
	client, recorded := recordingAPI(t, WatchList{
		Metadata: ListMeta{ResourceVersion: "1200"},
		Items: []Watch{{
			Metadata: ObjectMeta{Name: "the-girls", Namespace: "house"},
			Spec:     WatchSpec{People: []string{"chris"}, Item: WatchItem{Library: "series", Slug: "the-office"}},
		}},
	})

	list, err := ListWatches(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/apis/library.liken.sh/v1alpha1/watches")
	if len(list.Items) != 1 || list.Items[0].Spec.Item.Slug != "the-office" {
		t.Errorf("items = %+v, want the one Watch the server answered", list.Items)
	}
}

func TestPatchWatchOwnerReferencesSendsAConditionalMergePatch(t *testing.T) {
	client, recorded := recordingAPI(t, map[string]any{
		"metadata": map[string]any{"resourceVersion": "1201"},
	})

	version, err := PatchWatchOwnerReferences(t.Context(), client, "house", "the-girls", "1200",
		[]OwnerReference{{APIVersion: personAPIVersion, Kind: personKind, Name: "chris", UID: "chris-uid"}})
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPatch,
		"/apis/library.liken.sh/v1alpha1/namespaces/house/watches/the-girls")
	expectPatchBody(t, recorded, `"name":"chris"`, "finalizers")
	if version != "1201" {
		t.Errorf("resourceVersion = %q, want the one the write produced", version)
	}
}

// The status goes through its own subresource, so this request can
// never touch the spec a person declared.
func TestUpdateWatchStatusWritesTheStatusSubresource(t *testing.T) {
	written := &Watch{
		Metadata: ObjectMeta{Name: "the-girls", Namespace: "house", ResourceVersion: "1200"},
		Status:   WatchStatus{Play: "den-tv-b2k9x", Position: "0:12:00"},
	}
	client, recorded := recordingAPI(t, written)

	back, err := UpdateWatchStatus(t.Context(), client, written)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPut,
		"/apis/library.liken.sh/v1alpha1/namespaces/house/watches/the-girls/status")
	if !strings.Contains(recorded.body, `"resourceVersion":"1200"`) {
		t.Errorf("body = %s, want the resourceVersion that makes the write conditional", recorded.body)
	}
	if back.Status.Position != "0:12:00" {
		t.Errorf("position = %q, want the value the server wrote back", back.Status.Position)
	}
}

// The owner references are what tie a Watch to its people, so a patch
// the API server refuses leaves the status for the next pass too.
func TestAWatchWhoseOwnersDoNotLandIsNotWrittenEither(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedPerson(cluster, "chris")
	seedWatch(cluster, "the-girls", "chris")
	cluster.broken[http.MethodPatch+" "+watchPath(testLibraryNamespace, "the-girls")] =
		http.StatusInternalServerError
	publishProgress(operator, "the-girls", watchProgress{Play: "den-tv-b2k9x", Position: "0:12:00"})

	operator.pass()

	if writes := cluster.countRequests(http.MethodPut, "watches"); writes != 0 {
		t.Errorf("the pass wrote the status %d times, want none", writes)
	}
}

// A status write the API server refuses is reported, and the Watch
// keeps what it carries until the next pass.
func TestAStatusWriteTheAPIServerRefusesLeavesTheWatchAlone(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedWatch(cluster, "the-girls", "chris")
	cluster.broken[http.MethodPut+" "+watchPath(testLibraryNamespace, "the-girls")+"/status"] =
		http.StatusInternalServerError
	publishProgress(operator, "the-girls", watchProgress{Play: "den-tv-b2k9x", Position: "0:12:00"})

	operator.pass()

	if status := cluster.heldWatch("the-girls").Status; status != (WatchStatus{}) {
		t.Errorf("status = %+v, want the empty status the Watch carried", status)
	}
}

// The desk holds one projection per Watch, and an empty retained
// payload drops it.
func TestTheDeskFoldsAndDropsAWatchsProjection(t *testing.T) {
	operator := testOperator(t, newFakeCluster())
	topic := watchProgressTopic(defaultTopicBase, testLibraryNamespace, "the-girls")

	publishProgress(operator, "the-girls", watchProgress{Play: "den-tv-b2k9x", Item: 1})
	progress, held := operator.marks.progressFor(testLibraryNamespace, "the-girls")
	if !held || progress.Play != "den-tv-b2k9x" {
		t.Errorf("progress = %+v held %v, want the projection the store published", progress, held)
	}

	operator.handleBusMessage(topic, nil)
	if _, held := operator.marks.progressFor(testLibraryNamespace, "the-girls"); held {
		t.Error("the desk holds a projection the broker cleared")
	}
}
