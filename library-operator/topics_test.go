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
			name: "catalog availability",
			got:  catalogAvailabilityTopic(base, "house"),
			want: "liken/library/catalogs/house/availability",
		},
		{
			name: "catalog availability filter",
			got:  catalogAvailabilityFilter(base),
			want: "liken/library/catalogs/+/availability",
		},
		{
			name: "play",
			got:  playRequestTopic(base, "house", "den"),
			want: "liken/library/players/house/den/play",
		},
		{
			name: "play filter",
			got:  playRequestFilter(base),
			want: "liken/library/players/+/+/play",
		},
		{
			name: "audience",
			got:  audienceTopic(base, "house", "den"),
			want: "liken/library/players/house/den/audience",
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
			topic:     playRequestTopic(base, "house", "den"),
			namespace: "house",
			player:    "den",
			ok:        true,
		},
		{name: "a topic under another base", topic: "other/players/house/den/play"},
		{name: "the media operator's own tree", topic: "liken/media/players/house/den/commands"},
		{name: "a libraries topic", topic: libraryStatusTopic(base, "house", "movies")},
		{name: "a players topic with a kind this operator does not read", topic: base + "/players/house/den/screen"},
		{name: "the browser's own audience topic", topic: audienceTopic(base, "house", "den")},
		{name: "a players topic missing its name", topic: base + "/players/house/play"},
		{name: "a players topic with a level too many", topic: base + "/players/house/den/play/extra"},
		{name: "an empty namespace", topic: base + "/players//den/play"},
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

// The operator folds one reporter's availability by the namespace
// its topic names, and reads nothing off a topic of another shape.
func TestParseCatalogAvailabilityTopicNamesTheNamespace(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name      string
		topic     string
		namespace string
		ok        bool
	}{
		{
			name:      "a catalog availability topic",
			topic:     catalogAvailabilityTopic(base, "house"),
			namespace: "house",
			ok:        true,
		},
		{name: "a topic under another base", topic: "other/catalogs/house/availability"},
		{name: "a libraries topic", topic: libraryStatusTopic(base, "house", "movies")},
		{name: "a catalogs topic with a kind this operator does not read", topic: base + "/catalogs/house/status"},
		{name: "a catalogs topic with a level too many", topic: base + "/catalogs/house/movies/availability"},
		{name: "an empty namespace", topic: base + "/catalogs//availability"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			namespace, ok := parseCatalogAvailabilityTopic(base, each.topic)
			if ok != each.ok {
				t.Fatalf("ok = %v, want %v", ok, each.ok)
			}
			if namespace != each.namespace {
				t.Errorf("parsed %q, want %q", namespace, each.namespace)
			}
		})
	}
}

// A work list's topics name the Library, the fact, and the run, so a person
// reads one list with one filter, and the operator's filter reaches every
// list's count and no item.
func TestTheWorkListTopics(t *testing.T) {
	list := workList{namespace: "house", library: "series", fact: factAppearances, run: "series-walk-1"}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{name: "item", got: list.itemTopic(defaultTopicBase, 41),
			want: "liken/library/libraries/house/series/missing/appearances/series-walk-1/41"},
		{name: "count", got: list.countTopic(defaultTopicBase),
			want: "liken/library/libraries/house/series/missing/appearances/series-walk-1/count"},
		{name: "count filter", got: workCountFilter(defaultTopicBase),
			want: "liken/library/libraries/+/+/missing/+/+/count"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if one.got != one.want {
				t.Errorf("topic = %q, want %q", one.got, one.want)
			}
		})
	}
	if !topicMatches(workCountFilter(defaultTopicBase), list.countTopic(defaultTopicBase)) ||
		topicMatches(workCountFilter(defaultTopicBase), list.itemTopic(defaultTopicBase, 0)) {
		t.Error("the count filter must reach the count and no item")
	}
	if !topicMatches(libraryStatusFilter(defaultTopicBase), libraryStatusTopic(defaultTopicBase, "house", "series")) ||
		topicMatches(libraryStatusFilter(defaultTopicBase), list.countTopic(defaultTopicBase)) {
		t.Error("the status filter must reach no work list")
	}
}

// The operator reads the list back from a count topic, and refuses any
// other topic.
func TestParseWorkCountTopic(t *testing.T) {
	list := workList{namespace: "house", library: "series", fact: factTrickplay, run: "series-walk-1"}
	cases := []struct {
		name  string
		topic string
		ok    bool
	}{
		{name: "a count", topic: list.countTopic(defaultTopicBase), ok: true},
		{name: "an item", topic: list.itemTopic(defaultTopicBase, 3)},
		{name: "a status", topic: libraryStatusTopic(defaultTopicBase, "house", "series")},
		{name: "another base", topic: list.countTopic("other/base")},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got, ok := parseWorkCountTopic(defaultTopicBase, one.topic)
			if ok != one.ok || (ok && got != list) {
				t.Errorf("parse = %+v, %v, want %+v, %v", got, ok, list, one.ok)
			}
		})
	}
}
