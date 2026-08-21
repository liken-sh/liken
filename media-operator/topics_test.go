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
		{name: "play status", got: playStatusTopic(base, "house", "movie"), want: "liken/media/plays/house/movie/status"},
		{name: "play availability", got: playAvailabilityTopic(base, "house", "movie"), want: "liken/media/plays/house/movie/availability"},
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
