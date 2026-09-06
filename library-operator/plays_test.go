package main

// What these tests read: what the operator says about a Play that only
// it can say, and what it does with what the store says back. The Play
// is media-operator's object, so every write here is one merge patch of
// the metadata this operator owns.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A Play as media-operator holds it: in the namespace the seeded house
// keeps its library in, on the seeded screen, with the resourceVersion
// a conditional patch has to state.
func testPlay(name string) Play {
	return Play{
		Metadata: ObjectMeta{
			Name: name, Namespace: testLibraryNamespace,
			UID: name + "-uid", ResourceVersion: "1",
		},
		Spec: PlaySpec{Players: []string{testPlayer}},
	}
}

// The mark the store publishes for one Play, delivered the way the
// broker delivers it.
func publishRecorded(operator *operator, name string, recorded playRecorded) {
	payload, err := json.Marshal(recorded)
	if err != nil {
		panic(err)
	}
	operator.handleBusMessage(
		playRecordedTopic(defaultTopicBase, testLibraryNamespace, name), payload)
}

// The audience one publish carries.
func decodedAudience(t *testing.T, message brokerPublish) playAudience {
	t.Helper()
	audience := playAudience{}
	if err := json.Unmarshal(message.payload, &audience); err != nil {
		t.Fatal(err)
	}
	return audience
}

// The finalizer holds a Play open until the store has its last
// position, so the operator puts it on every Play a store records.
func TestAPassHoldsEveryPlayInAStoresNamespace(t *testing.T) {
	operator, cluster := playingHouse(t)
	cluster.plays = append(cluster.plays, testPlay("den-tv-some-film"))

	operator.pass()

	plays := cluster.heldPlays()
	if len(plays) != 1 || !plays[0].Metadata.holds(progressFinalizer) {
		t.Errorf("play = %+v, want it to hold %s", plays, progressFinalizer)
	}
}

// A namespace with no Catalog stands no store, and a finalizer nobody
// releases would hold the Play forever.
func TestAPlayOutsideAStoresNamespaceIsLeftAlone(t *testing.T) {
	operator, cluster := playingHouse(t)
	elsewhere := testPlay("den-tv-some-film")
	elsewhere.Metadata.Namespace = "studio"
	cluster.plays = append(cluster.plays, elsewhere)

	operator.pass()

	if plays := cluster.heldPlays(); plays[0].Metadata.holds(progressFinalizer) {
		t.Errorf("play = %+v, want no finalizer where no store stands", plays[0].Metadata)
	}
}

// The store cannot read a Play's metadata, so the operator publishes
// what the metadata says: the Player, the Watch, the people, the
// aliases, and the numbers of an episode.
func TestAPassPublishesWhoWatchedEachPlay(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	seedPlayer(cluster, testPlayer, testLibraryNamespace, screenController)
	play := testPlay("den-tv-the-office")
	play.Metadata.OwnerReferences = []OwnerReference{
		{APIVersion: libraryAPIVersion, Kind: watchKind, Name: "the-girls", UID: "the-girls-uid"},
		{APIVersion: personAPIVersion, Kind: personKind, Name: "thora", UID: "thora-uid"},
		{APIVersion: personAPIVersion, Kind: personKind, Name: "chris", UID: "chris-uid"},
	}
	play.Metadata.Annotations = map[string]string{
		libraryAnnotation:                "series",
		aliasAnnotationPrefix + "tmdb":   "2316",
		aliasAnnotationPrefix + "imdb":   "tt0386676",
		seasonAnnotation:                 "3",
		episodeAnnotation:                "5",
		"kubectl.kubernetes.io/last-app": "{}",
	}
	cluster.plays = append(cluster.plays, play)
	operator, broker := operatorOnABroker(t, cluster)

	operator.pass()

	message := waitForPublish(t, broker.pubs)
	want := playAudienceTopic(defaultTopicBase, testLibraryNamespace, "den-tv-the-office")
	if message.topic != want || !message.retained {
		t.Fatalf("published on %s (retained %v), want a retained message on %s",
			message.topic, message.retained, want)
	}
	audience := decodedAudience(t, message)
	if audience.Player != testPlayer || audience.Library != "series" || audience.Watch != "the-girls" {
		t.Errorf("audience = %+v, want the player, the library, and the watch", audience)
	}
	if len(audience.People) != 2 || audience.People[0] != "chris" || audience.People[1] != "thora" {
		t.Errorf("people = %v, want chris and thora in name order", audience.People)
	}
	if audience.Aliases["tmdb"] != "2316" || audience.Aliases["imdb"] != "tt0386676" {
		t.Errorf("aliases = %v, want the two the annotations carry", audience.Aliases)
	}
	if audience.Season != 3 || audience.Episode != 5 {
		t.Errorf("season and episode = %d and %d, want 3 and 5", audience.Season, audience.Episode)
	}
}

// A steady pass costs nothing on the bus: the audience goes out again
// only where it changed.
func TestAnUnchangedAudienceIsNotPublishedAgain(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	seedPlayer(cluster, testPlayer, testLibraryNamespace, screenController)
	cluster.plays = append(cluster.plays, testPlay("den-tv-some-film"))
	operator, broker := operatorOnABroker(t, cluster)

	operator.pass()
	waitForPublish(t, broker.pubs)
	operator.pass()
	operator.bus.Publish("liken/library/drills/marker", []byte("done"), false)

	if message := waitForPublish(t, broker.pubs); message.topic != "liken/library/drills/marker" {
		t.Errorf("the second pass published on %s, want nothing before the marker", message.topic)
	}
}

// The bus drops what nobody hears, so the operator publishes the last
// status off the API server for a Play that ended.
func TestAPassPublishesTheLastStatusOfAnEndedPlay(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	seedPlayer(cluster, testPlayer, testLibraryNamespace, screenController)
	play := testPlay("den-tv-some-film")
	play.Metadata.Finalizers = []string{progressFinalizer}
	play.Status = PlayStatus{Phase: playPhaseFinished, Item: 1, Position: "1:38:12", Duration: "1:40:00"}
	cluster.plays = append(cluster.plays, play)
	operator, broker := operatorOnABroker(t, cluster)

	operator.pass()

	waitForPublish(t, broker.pubs)
	message := waitForPublish(t, broker.pubs)
	want := playFinalTopic(defaultTopicBase, testLibraryNamespace, "den-tv-some-film")
	if message.topic != want || !message.retained {
		t.Fatalf("published on %s, want a retained message on %s", message.topic, want)
	}
	final := playFinal{}
	if err := json.Unmarshal(message.payload, &final); err != nil {
		t.Fatal(err)
	}
	if final.Phase != playPhaseFinished || final.Position != "1:38:12" || final.Duration != "1:40:00" {
		t.Errorf("final = %+v, want the status the Play carries", final)
	}
}

// A Play on its way out reports no further position, whatever phase it
// carries, so its status is published as final and the operator waits
// for the store's mark.
func TestADeletingPlayIsPublishedFinalAndHeldUntilItIsRecorded(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	seedPlayer(cluster, testPlayer, testLibraryNamespace, screenController)
	play := testPlay("den-tv-some-film")
	play.Metadata.Finalizers = []string{progressFinalizer}
	play.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
	play.Status = PlayStatus{Phase: "Running", Item: 1, Position: "0:12:00"}
	cluster.plays = append(cluster.plays, play)
	operator, broker := operatorOnABroker(t, cluster)

	operator.pass()

	waitForPublish(t, broker.pubs)
	final := playFinal{}
	if err := json.Unmarshal(waitForPublish(t, broker.pubs).payload, &final); err != nil {
		t.Fatal(err)
	}
	if final.Phase != "Running" || final.Position != "0:12:00" {
		t.Errorf("final = %+v, want the status the deleting Play carries", final)
	}
	if plays := cluster.heldPlays(); len(plays) != 1 {
		t.Errorf("plays = %+v, want the Play held until the store records it", plays)
	}
}

// The mark that the store wrote the last position is what releases the
// Play, and the three topics it stood on go with it.
func TestARecordedPlayLosesItsFinalizerAndItsTopics(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	seedPlayer(cluster, testPlayer, testLibraryNamespace, screenController)
	play := testPlay("den-tv-some-film")
	play.Metadata.Finalizers = []string{progressFinalizer}
	play.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
	play.Status = PlayStatus{Phase: playPhaseFinished, Item: 1, Position: "1:38:12"}
	cluster.plays = append(cluster.plays, play)
	operator, broker := operatorOnABroker(t, cluster)
	publishRecorded(operator, "den-tv-some-film", playRecorded{
		Item: 1, Position: "1:38:12", Ended: true, At: "2026-09-06T21:14:02Z",
	})

	operator.pass()

	if plays := cluster.heldPlays(); len(plays) != 0 {
		t.Errorf("plays = %+v, want the Play released", plays)
	}
	// The audience and the final go out before the release, and the
	// three clears follow them.
	waitForPublish(t, broker.pubs)
	waitForPublish(t, broker.pubs)
	cleared := clearedTopics(t, broker, 3)
	for _, topic := range playTopics(testLibraryNamespace, "den-tv-some-film") {
		if !cleared[topic] {
			t.Errorf("the release cleared %v, want %s among them", cleared, topic)
		}
	}
}

// A mark whose Play the pass does not see belongs to a Play that is
// gone, and its retained messages are standing on the bus still.
func TestTheTopicsOfAPlayThatIsGoneAreCleared(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	operator, broker := operatorOnABroker(t, cluster)
	publishRecorded(operator, "den-tv-gone", playRecorded{Item: 1, Ended: true})

	operator.pass()

	cleared := clearedTopics(t, broker, 3)
	for _, topic := range playTopics(testLibraryNamespace, "den-tv-gone") {
		if !cleared[topic] {
			t.Errorf("the pass cleared %v, want %s among them", cleared, topic)
		}
	}
	if _, held := operator.marks.recordedFor(testLibraryNamespace, "den-tv-gone"); held {
		t.Error("the desk still holds the mark of a Play that is gone")
	}
}

// PlayTopics names the three retained topics one Play stands on the
// bus, which is the set a release has to clear.
func playTopics(namespace, name string) []string {
	return []string{
		playAudienceTopic(defaultTopicBase, namespace, name),
		playFinalTopic(defaultTopicBase, namespace, name),
		playRecordedTopic(defaultTopicBase, namespace, name),
	}
}

// The desk holds what the store last said, and an empty retained
// payload is how the store says it holds nothing any more.
func TestTheDeskFoldsAndDropsWhatTheStoreRecorded(t *testing.T) {
	operator := testOperator(t, newFakeCluster())
	topic := playRecordedTopic(defaultTopicBase, testLibraryNamespace, "den-tv-some-film")

	publishRecorded(operator, "den-tv-some-film", playRecorded{Item: 2, Position: "0:12:00"})
	recorded, held := operator.marks.recordedFor(testLibraryNamespace, "den-tv-some-film")
	if !held || recorded.Item != 2 || recorded.Position != "0:12:00" {
		t.Errorf("recorded = %+v held %v, want the mark the store published", recorded, held)
	}

	operator.handleBusMessage(topic, nil)
	if _, held := operator.marks.recordedFor(testLibraryNamespace, "den-tv-some-film"); held {
		t.Error("the desk holds a mark the store cleared")
	}
}

// A payload that does not decode leaves the desk as it stands, because
// what it holds is still the last thing the store said.
func TestAMarkThatDoesNotDecodeLeavesTheDeskAlone(t *testing.T) {
	operator := testOperator(t, newFakeCluster())
	topic := playRecordedTopic(defaultTopicBase, testLibraryNamespace, "den-tv-some-film")
	publishRecorded(operator, "den-tv-some-film", playRecorded{Item: 2})

	operator.handleBusMessage(topic, []byte("{not json"))

	if recorded, held := operator.marks.recordedFor(testLibraryNamespace, "den-tv-some-film"); !held || recorded.Item != 2 {
		t.Errorf("recorded = %+v held %v, want the mark that decoded", recorded, held)
	}
}

// The audience of a Play with no owners and no annotations is the
// Player alone, because a fresh cluster has no Person and a Play is
// still a Play.
func TestAPlayWithNobodyOnItCarriesThePlayerAlone(t *testing.T) {
	play := testPlay("den-tv-some-film")

	audience := playAudienceOf(&play)

	if audience.Player != testPlayer || audience.Watch != "" || len(audience.People) != 0 {
		t.Errorf("audience = %+v, want the player alone", audience)
	}
	if audience.Season != 0 || audience.Episode != 0 || len(audience.Aliases) != 0 {
		t.Errorf("audience = %+v, want no aliases and no numbers", audience)
	}
}

// A Play is a person's to write, so a number annotation that holds
// anything else reads as none.
func TestANumberAnnotationThatIsNoNumberReadsAsNone(t *testing.T) {
	play := testPlay("den-tv-some-film")
	play.Metadata.Annotations = map[string]string{seasonAnnotation: "three"}

	if audience := playAudienceOf(&play); audience.Season != 0 {
		t.Errorf("season = %d, want 0", audience.Season)
	}
}

// A pass reads every Play in the cluster with one request, because
// progress is recorded for every Play and not only the ones a screen of
// this operator's asked for.
func TestListPlaysReadsEveryNamespace(t *testing.T) {
	client, recorded := recordingAPI(t, PlayList{
		Metadata: ListMeta{ResourceVersion: "1200"},
		Items:    []Play{{Metadata: ObjectMeta{Name: "den-tv-some-film", Namespace: "house"}}},
	})

	list, err := ListPlays(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/apis/media.liken.sh/v1alpha1/plays")
	if len(list.Items) != 1 || list.Items[0].Metadata.Name != "den-tv-some-film" {
		t.Errorf("items = %+v, want the one Play the server answered", list.Items)
	}
}

// The patch states the finalizer list always, because taking a
// finalizer off is a write of the shorter list, and it states the owner
// references and the annotations only where the caller gives them.
func TestPatchPlayMetadataSendsAConditionalMergePatch(t *testing.T) {
	cases := []struct {
		name     string
		metadata ObjectMeta
		want     string
		absent   string
	}{
		{
			name:     "the finalizer alone",
			metadata: ObjectMeta{Finalizers: []string{progressFinalizer}},
			want:     `"finalizers":["library.liken.sh/progress"]`,
			absent:   "ownerReferences",
		},
		{
			name: "the audience beside it",
			metadata: ObjectMeta{
				Finalizers:      []string{progressFinalizer},
				OwnerReferences: []OwnerReference{{Kind: personKind, Name: "chris"}},
				Annotations:     map[string]string{seasonAnnotation: "3"},
			},
			want: `"kind":"Person"`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, recorded := recordingAPI(t, map[string]any{
				"metadata": map[string]any{"resourceVersion": "1201"},
			})

			version, err := PatchPlayMetadata(t.Context(), client, "house", "den-tv-some-film",
				"1200", testCase.metadata)
			if err != nil {
				t.Fatal(err)
			}

			expectRequest(t, recorded, http.MethodPatch,
				"/apis/media.liken.sh/v1alpha1/namespaces/house/plays/den-tv-some-film")
			expectPatchBody(t, recorded, testCase.want, testCase.absent)
			if version != "1201" {
				t.Errorf("resourceVersion = %q, want the one the write produced", version)
			}
		})
	}
}

// expectPatchBody reads the merge patch a verb sent: the
// resourceVersion that makes it conditional, one field it has to
// carry, and one it must leave out.
func expectPatchBody(t *testing.T, recorded *recordedRequest, want, absent string) {
	t.Helper()
	if !strings.Contains(recorded.body, `"resourceVersion":"1200"`) {
		t.Errorf("body = %s, want the resourceVersion that makes the write conditional", recorded.body)
	}
	if !strings.Contains(recorded.body, want) {
		t.Errorf("body = %s, want it to carry %s", recorded.body, want)
	}
	if absent != "" && strings.Contains(recorded.body, absent) {
		t.Errorf("body = %s, want no %s in it", recorded.body, absent)
	}
}

// A cluster with no media-operator serves no Plays, and one with no
// people-operator serves no people. The pass reports that and
// reconciles every Library as it would otherwise.
func TestPassCarriesOnWithNoProgressCollectionsToRead(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the plays cannot be listed", path: playsAllPath},
		{name: "the people cannot be listed", path: peoplePath},
		{name: "the watches cannot be listed", path: watchesPath},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			boundHouse(cluster)
			cluster.broken[one.path] = http.StatusNotFound

			testOperator(t, cluster).pass()

			if cluster.heldLibrary("movies").Status.Conditions == nil {
				t.Error("the pass wrote no status for the library")
			}
		})
	}
}

// A patch the API server refuses is reported and the pass carries on,
// because one Play must not stop the record of the others.
func TestAHoldTheAPIServerRefusesLeavesThePlayAlone(t *testing.T) {
	operator, cluster := playingHouse(t)
	cluster.plays = append(cluster.plays, testPlay("den-tv-some-film"))
	cluster.broken[http.MethodPatch+" "+playPath(testLibraryNamespace, "den-tv-some-film")] =
		http.StatusInternalServerError

	operator.pass()

	if plays := cluster.heldPlays(); plays[0].Metadata.holds(progressFinalizer) {
		t.Errorf("play = %+v, want the finalizer left for the next pass", plays[0].Metadata)
	}
}

// The three answers a release can get. Only a Play that is already gone
// is the state the release was for, and the other two leave the mark
// standing for the next pass.
func TestAReleaseThatDoesNotLandLeavesTheMarkStanding(t *testing.T) {
	cases := []struct {
		name   string
		status int
		held   bool
	}{
		{name: "a play that is already gone", status: http.StatusNotFound, held: false},
		{name: "a write that raced another pass", status: http.StatusConflict, held: true},
		{name: "an api server that failed", status: http.StatusInternalServerError, held: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			operator, cluster := playingHouse(t)
			play := testPlay("den-tv-some-film")
			play.Metadata.Finalizers = []string{progressFinalizer}
			play.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
			cluster.plays = append(cluster.plays, play)
			cluster.broken[http.MethodPatch+" "+playPath(testLibraryNamespace, "den-tv-some-film")] =
				testCase.status
			publishRecorded(operator, "den-tv-some-film", playRecorded{Item: 1, Ended: true})

			operator.pass()

			_, held := operator.marks.recordedFor(testLibraryNamespace, "den-tv-some-film")
			if held != testCase.held {
				t.Errorf("the desk holds the mark: %v, want %v", held, testCase.held)
			}
		})
	}
}

// A namespace that stands no store records nothing, so a Play there
// that carries the finalizer is one nobody would ever release. A
// namespace with two Catalogs stands no store either, which is the rule
// the catalog pod stands on.
func TestAPlayNoStoreWillRecordIsReleased(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(cluster *fakeCluster)
	}{
		{
			name:    "a namespace with no catalog",
			arrange: func(cluster *fakeCluster) { delete(cluster.catalogs, "house-catalog") },
		},
		{
			name:    "a namespace with two catalogs",
			arrange: func(cluster *fakeCluster) { seedCatalog(cluster, "second-catalog", testLibraryNamespace) },
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			operator, cluster := playingHouse(t)
			play := testPlay("den-tv-some-film")
			play.Metadata.Finalizers = []string{progressFinalizer}
			cluster.plays = append(cluster.plays, play)
			testCase.arrange(cluster)

			operator.pass()

			held := cluster.heldPlays()
			if len(held) != 1 || held[0].Metadata.holds(progressFinalizer) {
				t.Errorf("play = %+v, want the finalizer released", held)
			}
		})
	}
}
