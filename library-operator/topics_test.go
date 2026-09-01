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
			got:  libraryStatusTopic(base, "house", "movies"),
			want: "liken/library/libraries/house/movies/status",
		},
		{
			name: "availability",
			got:  libraryAvailabilityTopic(base, "house", "movies"),
			want: "liken/library/libraries/house/movies/availability",
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
		{
			name: "play",
			got:  playRequestTopic(base, "house", "den-tv"),
			want: "liken/library/players/house/den-tv/play",
		},
		{
			name: "play filter",
			got:  playRequestFilter(base),
			want: "liken/library/players/+/+/play",
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
			topic:     libraryStatusTopic(base, "house", "movies"),
			namespace: "house",
			library:   "movies",
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
		{name: "a topic under another base", topic: "other/libraries/house/movies/status"},
		{name: "the media operator's own tree", topic: "liken/media/plays/house/movie/status"},
		{name: "a libraries topic with a kind this operator does not read", topic: base + "/libraries/house/movies/commands"},
		{name: "a libraries topic missing its name", topic: base + "/libraries/house/status"},
		{name: "a libraries topic with a level too many", topic: base + "/libraries/house/movies/status/extra"},
		{name: "an empty namespace", topic: base + "/libraries//movies/status"},
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

func TestParsePlayRequestTopicNamesThePlayer(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name      string
		topic     string
		namespace string
		player    string
		ok        bool
	}{
		{
			name:      "a play topic",
			topic:     playRequestTopic(base, "house", "den-tv"),
			namespace: "house",
			player:    "den-tv",
			ok:        true,
		},
		{name: "a topic under another base", topic: "other/players/house/den-tv/play"},
		{name: "the media operator's own tree", topic: "liken/media/players/house/den-tv/commands"},
		{name: "a libraries topic", topic: libraryStatusTopic(base, "house", "movies")},
		{name: "a players topic with a kind this operator does not read", topic: base + "/players/house/den-tv/screen"},
		{name: "a players topic missing its name", topic: base + "/players/house/play"},
		{name: "a players topic with a level too many", topic: base + "/players/house/den-tv/play/extra"},
		{name: "an empty namespace", topic: base + "/players//den-tv/play"},
		{name: "an empty name", topic: base + "/players/house//play"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			namespace, player, ok := parsePlayRequestTopic(base, each.topic)
			if ok != each.ok {
				t.Fatalf("ok = %v, want %v", ok, each.ok)
			}
			if !ok {
				return
			}
			if namespace != each.namespace || player != each.player {
				t.Errorf("parsed (%q, %q), want (%q, %q)",
					namespace, player, each.namespace, each.player)
			}
		})
	}
}
