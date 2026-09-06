package main

// What these tests read: the finalizer the operator holds on every
// Person, and the ask that every namespace's store drop that person's
// rows before the object goes.

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A Person as people-operator holds it, with the resourceVersion a
// conditional patch has to state.
func seedPerson(cluster *fakeCluster, name string) *Person {
	person := &Person{
		Metadata: ObjectMeta{Name: name, UID: name + "-uid", ResourceVersion: "1"},
		Spec:     PersonSpec{DisplayName: name},
	}
	cluster.people[name] = person
	return person
}

// One namespace's answer that the person's rows are gone, delivered the
// way the broker delivers it.
func publishForgotten(operator *operator, person, namespace string) {
	operator.handleBusMessage(
		personForgottenTopic(defaultTopicBase, person, namespace), []byte(`{}`))
}

// The finalizer is what keeps a delete from outrunning the sweep of a
// person's rows.
func TestAPassHoldsEveryPerson(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedPerson(cluster, "chris")

	operator.pass()

	if person := cluster.heldPerson("chris"); person == nil || !person.Metadata.holds(progressFinalizer) {
		t.Errorf("person = %+v, want it to hold %s", person, progressFinalizer)
	}
}

// A deleting Person is asked for once, on a topic every namespace's
// store reads, and the object stays until they all answer.
func TestADeletingPersonIsAskedForOnceAndHeld(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	person := seedPerson(cluster, "chris")
	person.Metadata.Finalizers = []string{progressFinalizer}
	person.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
	operator, broker := operatorOnABroker(t, cluster)

	operator.pass()
	operator.pass()
	operator.bus.Publish("liken/library/drills/marker", []byte("done"), false)

	message := waitForPublish(t, broker.pubs)
	if message.topic != personForgetTopic(defaultTopicBase, "chris") || !message.retained {
		t.Fatalf("published on %s, want a retained ask on the forget topic", message.topic)
	}
	ask := personForgetRequest{}
	if err := json.Unmarshal(message.payload, &ask); err != nil {
		t.Fatal(err)
	}
	if ask.At == "" {
		t.Errorf("ask = %+v, want the time the operator asked", ask)
	}
	if next := waitForPublish(t, broker.pubs); next.topic != "liken/library/drills/marker" {
		t.Errorf("the second pass published on %s, want nothing before the marker", next.topic)
	}
	if cluster.heldPerson("chris") == nil {
		t.Error("the person is gone before any store answered")
	}
}

// Every namespace that holds a Catalog has to answer before the Person
// goes, and the two topics the exchange stood on go with it.
func TestAForgottenPersonIsReleasedAndTheTopicsCleared(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	person := seedPerson(cluster, "chris")
	person.Metadata.Finalizers = []string{progressFinalizer}
	person.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
	operator, broker := operatorOnABroker(t, cluster)
	publishForgotten(operator, "chris", testLibraryNamespace)

	operator.pass()

	if cluster.heldPerson("chris") != nil {
		t.Error("the person is held after every store answered")
	}
	waitForPublish(t, broker.pubs)
	cleared := clearedTopics(t, broker, 2)
	for _, topic := range []string{
		personForgetTopic(defaultTopicBase, "chris"),
		personForgottenTopic(defaultTopicBase, "chris", testLibraryNamespace),
	} {
		if !cleared[topic] {
			t.Errorf("the release cleared %v, want %s among them", cleared, topic)
		}
	}
	if operator.marks.forgottenBy("chris") != nil {
		t.Error("the desk still holds the answers about a person that is gone")
	}
}

// A namespace that has not answered holds the Person, because that
// namespace's store still has their rows.
func TestAPersonWaitsOnEveryNamespaceThatHoldsACatalog(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	boundStudio(cluster)
	person := seedPerson(cluster, "chris")
	person.Metadata.Finalizers = []string{progressFinalizer}
	person.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
	operator := testOperator(t, cluster)
	publishForgotten(operator, "chris", testLibraryNamespace)

	operator.pass()

	if cluster.heldPerson("chris") == nil {
		t.Error("the person is gone while one namespace has not answered")
	}
}

// The desk holds one answer per namespace, and the operator's own clear
// comes back to it, which drops the answer.
func TestTheDeskFoldsAndDropsAnAnswerAboutAPerson(t *testing.T) {
	operator := testOperator(t, newFakeCluster())

	publishForgotten(operator, "chris", testLibraryNamespace)
	if !operator.marks.forgottenBy("chris")[testLibraryNamespace] {
		t.Error("the desk holds no answer for the namespace that answered")
	}

	operator.handleBusMessage(
		personForgottenTopic(defaultTopicBase, "chris", testLibraryNamespace), nil)
	if operator.marks.forgottenBy("chris")[testLibraryNamespace] {
		t.Error("the desk holds an answer the broker cleared")
	}
}

// A Person is cluster-scoped, so the collection path carries no
// namespace.
func TestListPeopleReadsTheClusterScopedCollection(t *testing.T) {
	client, recorded := recordingAPI(t, PersonList{
		Metadata: ListMeta{ResourceVersion: "1200"},
		Items:    []Person{{Metadata: ObjectMeta{Name: "chris"}, Spec: PersonSpec{DisplayName: "Chris"}}},
	})

	list, err := ListPeople(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/apis/people.liken.sh/v1alpha1/people")
	if len(list.Items) != 1 || list.Items[0].Spec.DisplayName != "Chris" {
		t.Errorf("items = %+v, want the one Person the server answered", list.Items)
	}
}

func TestPatchPersonFinalizersSendsAConditionalMergePatch(t *testing.T) {
	client, recorded := recordingAPI(t, map[string]any{
		"metadata": map[string]any{"resourceVersion": "1201"},
	})

	version, err := PatchPersonFinalizers(t.Context(), client, "chris", "1200",
		[]string{progressFinalizer})
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPatch, "/apis/people.liken.sh/v1alpha1/people/chris")
	expectPatchBody(t, recorded, `"finalizers":["library.liken.sh/progress"]`, "ownerReferences")
	if version != "1201" {
		t.Errorf("resourceVersion = %q, want the one the write produced", version)
	}
}

// A patch the API server refuses is reported and the pass carries on,
// so the finalizer goes on at the next pass.
func TestAHoldTheAPIServerRefusesLeavesThePersonAlone(t *testing.T) {
	operator, cluster := playingHouse(t)
	seedPerson(cluster, "chris")
	cluster.broken[http.MethodPatch+" "+personPath("chris")] = http.StatusInternalServerError

	operator.pass()

	if person := cluster.heldPerson("chris"); person.Metadata.holds(progressFinalizer) {
		t.Errorf("person = %+v, want the finalizer left for the next pass", person.Metadata)
	}
}

// A release that races another pass, and one the API server refuses,
// both leave the Person for the next pass.
func TestAPersonWhoseReleaseDoesNotLandIsHeld(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{name: "a write that raced another pass", status: http.StatusConflict},
		{name: "an api server that failed", status: http.StatusInternalServerError},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := newFakeCluster()
			boundHouse(cluster)
			person := seedPerson(cluster, "chris")
			person.Metadata.Finalizers = []string{progressFinalizer}
			person.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
			cluster.broken[http.MethodPatch+" "+personPath("chris")] = testCase.status
			operator := testOperator(t, cluster)
			publishForgotten(operator, "chris", testLibraryNamespace)

			operator.pass()

			if operator.marks.forgottenBy("chris") == nil {
				t.Error("the desk dropped the answers about a person the operator still holds")
			}
		})
	}
}
