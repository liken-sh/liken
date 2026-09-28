package main

// what these tests read: the desk that holds whether each namespace's
// reporter is on the bus.

import (
	"testing"
	"time"
)

// a namespace the desk holds nothing for reads offline, which is the
// state before its catalog pod first connects.
func TestReportersReadOfflineUntilTheyConnect(t *testing.T) {
	desk := newReporters(make(chan struct{}, 1))

	if desk.onlineFor("house") {
		t.Error("a namespace nothing was said about reads online")
	}
	desk.mark("house", true)
	if !desk.onlineFor("house") {
		t.Error("the namespace reads offline after its reporter connected")
	}
	if desk.onlineFor("studio") {
		t.Error("one namespace's reporter marked another namespace online")
	}
}

// a change of the flag wakes the loop, and a repeat wakes nothing,
// because a reconnecting reporter republishes online on every session.
func TestReportersWakeTheLoopOnAChangeAlone(t *testing.T) {
	woken := make(chan struct{}, 1)
	desk := newReporters(woken)

	desk.mark("house", true)
	if !woke(woken) {
		t.Fatal("the first mark woke no pass")
	}
	desk.mark("house", true)
	if woke(woken) {
		t.Error("a repeat of the flag woke a pass")
	}
	desk.mark("house", false)
	if !woke(woken) {
		t.Error("the reporter leaving the bus woke no pass")
	}
}

// woke reads the wake channel without blocking the test.
func woke(wake chan struct{}) bool {
	select {
	case <-wake:
		return true
	case <-time.After(50 * time.Millisecond):
		return false
	}
}

// a topic of another shape names no namespace, so the operator folds
// nothing from it.
func TestParseCatalogAvailabilityTopicReadsItsOwnShape(t *testing.T) {
	cases := []struct {
		name      string
		topic     string
		namespace string
	}{
		{name: "another tree", topic: "liken/media/catalogs/house/availability"},
		{name: "another kind", topic: defaultTopicBase + "/catalogs/house/status"},
		{name: "no namespace", topic: defaultTopicBase + "/catalogs//availability"},
		{name: "one level too many", topic: defaultTopicBase + "/catalogs/house/one/availability"},
		{name: "its own", topic: catalogAvailabilityTopic(defaultTopicBase, "house"), namespace: "house"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			namespace, ok := parseCatalogAvailabilityTopic(defaultTopicBase, one.topic)

			if ok != (one.namespace != "") {
				t.Fatalf("ok = %v, want %v", ok, one.namespace != "")
			}
			if namespace != one.namespace {
				t.Errorf("namespace = %q, want %q", namespace, one.namespace)
			}
		})
	}
}
