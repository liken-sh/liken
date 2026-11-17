package main

// What these tests prove: the operator releases an object only after the
// bus has taken the clear of each retained topic that stood for it. The
// clear is published once, and after the release no pass reads the object
// again, so a clear that the bus dropped would stay on the broker. A forget
// request that stays there deletes the progress of a later Person with the
// same name, each time a progress pod subscribes.

import (
	"testing"
	"testing/synctest"
)

// forgottenPerson is a deleting Person that every store has answered for,
// on an operator whose bus is connected to a broker that retains messages.
// The broker already retains the forget request.
func forgottenPerson(t *testing.T, name string) (*operator, *retainBroker, *fakeCluster) {
	t.Helper()
	cluster := newFakeCluster()
	boundHouse(cluster)
	person := seedPerson(cluster, name)
	person.Metadata.Finalizers = []string{progressFinalizer}
	person.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
	operator, broker := operatorOnARetainBroker(t, cluster)
	go operator.bus.Run(t.Context())
	synctest.Wait()
	operator.pass()
	synctest.Wait()
	publishForgotten(operator, name, testLibraryNamespace)
	return operator, broker, cluster
}

// fillTheQueue stops the broker's reads and publishes more marks than the
// bus's write queue holds. It answers with the release of the reads.
func fillTheQueue(operator *operator, broker *retainBroker) (release func()) {
	release = broker.holdReads()
	for topic, mark := range manyMarks() {
		operator.publishMark(topic, mark)
	}
	return release
}

func TestAPersonIsHeldWhileTheBusCannotTakeTheClearOfTheForgetRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operator, broker, cluster := forgottenPerson(t, "person-gone")
		release := fillTheQueue(operator, broker)
		t.Cleanup(release)

		operator.pass()

		if cluster.heldPerson("person-gone") == nil {
			t.Error("the person was released while the broker still retains the forget request")
		}
	})
}

func TestAPersonIsReleasedOnceTheBusTakesTheClearOfTheForgetRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operator, broker, cluster := forgottenPerson(t, "person-gone")
		release := fillTheQueue(operator, broker)
		operator.pass()

		release()
		synctest.Wait()
		operator.pass()
		synctest.Wait()

		if cluster.heldPerson("person-gone") != nil {
			t.Error("the person is held after the broker read again")
		}
		topic := personForgetTopic(defaultTopicBase, "person-gone")
		if _, held := broker.retainedOn(topic); held {
			t.Errorf("the broker still retains %s after the release", topic)
		}
	})
}

// The store's recorded mark is what leads a later pass to the topics of a
// Play that is gone, so the mark outlasts a clear that the bus did not take.
func TestTheTopicsOfAReleasedPlayAreClearedOnceTheBusTakesTheClears(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		boundHouse(cluster)
		seedPlayer(cluster, testPlayer, testLibraryNamespace, screenController)
		play := testPlay("den-some-film")
		play.Metadata.Finalizers = []string{progressFinalizer}
		play.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
		cluster.plays = append(cluster.plays, play)
		operator, broker := operatorOnARetainBroker(t, cluster)
		go operator.bus.Run(t.Context())
		synctest.Wait()
		recorded := playRecordedTopic(defaultTopicBase, testLibraryNamespace, "den-some-film")
		operator.bus.Publish(recorded, []byte(`{"item":1,"ended":true}`), true)
		synctest.Wait()
		publishRecorded(operator, "den-some-film", playRecorded{Item: 1, Ended: true})
		release := fillTheQueue(operator, broker)
		operator.pass()

		release()
		synctest.Wait()
		operator.pass()
		synctest.Wait()

		if _, held := broker.retainedOn(recorded); held {
			t.Errorf("the broker still retains %s after the Play was released", recorded)
		}
	})
}
