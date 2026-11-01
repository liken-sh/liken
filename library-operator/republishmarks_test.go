package main

// What these tests prove: every retained mark the operator publishes is
// retained on the broker again after each connect. The bus runs with
// persistence off, so a broker that restarts holds no retained message,
// and a publish made while the client is disconnected is dropped. A
// progress pod or a reporter that subscribes after either one must still
// read the current marks.

import (
	"testing"
	"testing/synctest"
	"time"
)

// operatorOnARetainBroker is the operator of testOperator with the bus
// newOperator built, dialing a broker that retains messages. The bus is
// not started, so a test decides when the first connect happens.
func operatorOnARetainBroker(t *testing.T, cluster *fakeCluster) (*operator, *retainBroker) {
	t.Helper()
	operator := testOperator(t, cluster)
	broker := newRetainBroker(t)
	operator.bus.dial = broker.dial
	return operator, broker
}

// refreshedHouse is the house Library with a credits refresh, so a pass
// publishes a refresh mark for it. It answers with the mark's topic.
func refreshedHouse(cluster *fakeCluster) string {
	library := boundHouse(cluster)
	library.Spec.Refresh = map[string]time.Time{factCredits: testNow}
	return libraryRefreshTopic(defaultTopicBase, "house", "movies")
}

func TestAPassAfterABrokerRestartRetainsTheMarksAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		topic := refreshedHouse(cluster)
		operator, broker := operatorOnARetainBroker(t, cluster)
		go operator.bus.Run(t.Context())
		synctest.Wait()
		operator.pass()
		synctest.Wait()
		if _, held := broker.retainedOn(topic); !held {
			t.Fatalf("the broker retains nothing on %s before it restarted", topic)
		}

		broker.restart()
		time.Sleep(2 * busMinBackoff)
		synctest.Wait()
		operator.pass()
		synctest.Wait()

		if _, held := broker.retainedOn(topic); !held {
			t.Errorf("the broker retains nothing on %s after it restarted", topic)
		}
	})
}

func TestAPassAfterTheFirstConnectRetainsAMarkPublishedBeforeIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		topic := refreshedHouse(cluster)
		operator, broker := operatorOnARetainBroker(t, cluster)
		operator.pass()

		go operator.bus.Run(t.Context())
		synctest.Wait()
		operator.pass()
		synctest.Wait()

		if _, held := broker.retainedOn(topic); !held {
			t.Errorf("the broker retains nothing on %s after the first connect", topic)
		}
	})
}
