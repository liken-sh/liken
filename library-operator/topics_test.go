package main

// These tests hold the bus contract still. A topic string is what a
// scanner, the operator, and any other program on the broker agree
// on, so a change to one of these strings is a change every party
// must make together.

import "testing"

func TestTopicsCarryTheLibraryLayout(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "status",
			got:  libraryStatusTopic(base, "house", "films"),
			want: "liken/library/libraries/house/films/status",
		},
		{
			name: "availability",
			got:  libraryAvailabilityTopic(base, "house", "films"),
			want: "liken/library/libraries/house/films/availability",
		},
		{
			name: "status filter",
			got:  libraryStatusFilter(base),
			want: "liken/library/libraries/+/+/status",
		},
		{
			name: "availability filter",
			got:  libraryAvailabilityFilter(base),
			want: "liken/library/libraries/+/+/availability",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			if each.got != each.want {
				t.Errorf("topic = %q, want %q", each.got, each.want)
			}
		})
	}
}

func TestParseLibraryTopicNamesTheLibraryAndTheKind(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name      string
		topic     string
		namespace string
		library   string
		kind      string
		ok        bool
	}{
		{
			name:      "a status topic",
			topic:     libraryStatusTopic(base, "house", "films"),
			namespace: "house",
			library:   "films",
			kind:      libraryStatusKind,
			ok:        true,
		},
		{
			name:      "an availability topic",
			topic:     libraryAvailabilityTopic(base, "attic", "series"),
			namespace: "attic",
			library:   "series",
			kind:      libraryAvailabilityKind,
			ok:        true,
		},
		{name: "a topic under another base", topic: "other/libraries/house/films/status"},
		{name: "the media operator's own tree", topic: "liken/media/plays/house/movie/status"},
		{name: "a libraries topic with a kind this operator does not read", topic: base + "/libraries/house/films/commands"},
		{name: "a libraries topic missing its name", topic: base + "/libraries/house/status"},
		{name: "a libraries topic with a level too many", topic: base + "/libraries/house/films/status/extra"},
		{name: "an empty namespace", topic: base + "/libraries//films/status"},
		{name: "an empty name", topic: base + "/libraries/house//status"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			namespace, library, kind, ok := parseLibraryTopic(base, each.topic)
			if ok != each.ok {
				t.Fatalf("ok = %v, want %v", ok, each.ok)
			}
			if !ok {
				return
			}
			if namespace != each.namespace || library != each.library || kind != each.kind {
				t.Errorf("parsed (%q, %q, %q), want (%q, %q, %q)",
					namespace, library, kind, each.namespace, each.library, each.kind)
			}
		})
	}
}

// A topic a builder made parses back to the Library it names, so the
// publishing side and the reading side cannot drift apart.
func TestATopicRoundTripsThroughTheParser(t *testing.T) {
	namespace, name, kind, ok := parseLibraryTopic(
		defaultTopicBase,
		libraryStatusTopic(defaultTopicBase, "attic", "series"),
	)

	if !ok {
		t.Fatal("a topic this file built did not parse")
	}
	if namespace != "attic" || name != "series" || kind != libraryStatusKind {
		t.Errorf("parsed (%q, %q, %q), want (attic, series, status)", namespace, name, kind)
	}
}
