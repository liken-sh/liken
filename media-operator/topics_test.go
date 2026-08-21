package main

// These tests cover the topic layout: every builder against a known
// base, and the parse that maps an inbound plays topic back to the
// Play it names.

import "testing"

func TestTopicBuildersExtendTheBase(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name string
		got  string
		want string
	}{
		{name: "remote events", got: remoteEventsTopic(base, "house", "sofa"), want: "liken/media/remotes/house/sofa/events"},
		{name: "remote focus", got: remoteFocusTopic(base, "house", "sofa"), want: "liken/media/remotes/house/sofa/focus"},
		{name: "remote focus cycle", got: remoteFocusCycleTopic(base, "house", "sofa"), want: "liken/media/remotes/house/sofa/focus/cycle"},
		{name: "focus filter", got: remoteFocusFilter(base), want: "liken/media/remotes/+/+/focus"},
		{name: "focus cycle filter", got: remoteFocusCycleFilter(base), want: "liken/media/remotes/+/+/focus/cycle"},
		{name: "play status", got: playStatusTopic(base, "house", "movie"), want: "liken/media/plays/house/movie/status"},
		{name: "play availability", got: playAvailabilityTopic(base, "house", "movie"), want: "liken/media/plays/house/movie/availability"},
		{name: "play commands", got: playCommandsTopic(base, "house", "movie"), want: "liken/media/plays/house/movie/commands"},
		{name: "keymap", got: keymapTopic(base, "gamepad"), want: "liken/media/keymaps/gamepad"},
		{name: "status filter", got: playStatusFilter(base), want: "liken/media/plays/+/+/status"},
		{name: "availability filter", got: playAvailabilityFilter(base), want: "liken/media/plays/+/+/availability"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			if each.got != each.want {
				t.Errorf("topic = %q, want %q", each.got, each.want)
			}
		})
	}
}

func TestParsePlayTopicNamesThePlayAndTheKind(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name      string
		topic     string
		namespace string
		play      string
		kind      string
		ok        bool
	}{
		{
			name:      "a status topic",
			topic:     playStatusTopic(base, "house", "movie"),
			namespace: "house",
			play:      "movie",
			kind:      playStatusKind,
			ok:        true,
		},
		{
			name:      "an availability topic",
			topic:     playAvailabilityTopic(base, "attic", "radio"),
			namespace: "attic",
			play:      "radio",
			kind:      playAvailabilityKind,
			ok:        true,
		},
		{name: "a remote topic under the same base", topic: remoteEventsTopic(base, "house", "sofa")},
		{name: "a topic under another base", topic: "other/plays/house/movie/status"},
		{name: "a plays topic with a kind this operator does not read", topic: base + "/plays/house/movie/focus"},
		{name: "a plays topic missing its name", topic: base + "/plays/house/status"},
		{name: "an empty namespace", topic: base + "/plays//movie/status"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			namespace, play, kind, ok := parsePlayTopic(base, each.topic)
			if ok != each.ok {
				t.Fatalf("ok = %v, want %v", ok, each.ok)
			}
			if !ok {
				return
			}
			if namespace != each.namespace || play != each.play || kind != each.kind {
				t.Errorf("parsed (%q, %q, %q), want (%q, %q, %q)",
					namespace, play, kind, each.namespace, each.play, each.kind)
			}
		})
	}
}

// The focus parser matches only a focus mark and the cycle parser only a
// cycle request. Neither matches the other or the events topic.
func TestTheFocusParsersDoNotCrossMatch(t *testing.T) {
	base := defaultTopicBase
	focus := remoteFocusTopic(base, "house", "sofa")
	cycle := remoteFocusCycleTopic(base, "house", "sofa")
	events := remoteEventsTopic(base, "house", "sofa")

	if ns, name, ok := parseRemoteFocusTopic(base, focus); !ok || ns != "house" || name != "sofa" {
		t.Errorf("focus parse of %q = (%q, %q, %v), want house/sofa true", focus, ns, name, ok)
	}
	if _, _, ok := parseRemoteFocusTopic(base, cycle); ok {
		t.Errorf("the focus parser matched the cycle topic %q", cycle)
	}
	if _, _, ok := parseRemoteFocusTopic(base, events); ok {
		t.Errorf("the focus parser matched the events topic %q", events)
	}

	if ns, name, ok := parseRemoteFocusCycleTopic(base, cycle); !ok || ns != "house" || name != "sofa" {
		t.Errorf("cycle parse of %q = (%q, %q, %v), want house/sofa true", cycle, ns, name, ok)
	}
	if _, _, ok := parseRemoteFocusCycleTopic(base, focus); ok {
		t.Errorf("the cycle parser matched the focus topic %q", focus)
	}
	if _, _, ok := parseRemoteFocusCycleTopic(base, events); ok {
		t.Errorf("the cycle parser matched the events topic %q", events)
	}
}
