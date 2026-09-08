package main

// What these tests hold still: the topic one outside play travels on, the
// kind the parser reads back off it, and the field names the payload carries.

import (
	"encoding/json"
	"testing"
)

func TestTheOutsideTopicsCarryThePlayLayout(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "one play",
			got:  playOutsideTopic(base, "house", "jellyfin-7-19"),
			want: "liken/library/plays/house/jellyfin-7-19/outside",
		},
		{
			name: "one namespace's plays",
			got:  playOutsideFilter(base, "house"),
			want: "liken/library/plays/house/+/outside",
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

// The parser reads the outside kind off the topic the helper built, so the
// publishing side and the reading side cannot drift apart.
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
			name:      "an outside topic",
			topic:     playOutsideTopic(base, "house", "jellyfin-7-19"),
			namespace: "house",
			play:      "jellyfin-7-19",
			kind:      playOutsideKind,
			ok:        true,
		},
		{
			name:      "an audience topic",
			topic:     playAudienceTopic(base, "attic", "play-1"),
			namespace: "attic",
			play:      "play-1",
			kind:      playAudienceKind,
			ok:        true,
		},
		{name: "a topic under another base", topic: "other/plays/house/play-1/outside"},
		{name: "a topic of another branch", topic: base + "/people/chris/forget"},
		{name: "a plays topic missing its kind", topic: base + "/plays/house/play-1"},
		{name: "a plays topic with a level too many", topic: base + "/plays/house/play-1/outside/extra"},
		{name: "an empty namespace", topic: base + "/plays//play-1/outside"},
		{name: "an empty name", topic: base + "/plays/house//outside"},
		{name: "an empty kind", topic: base + "/plays/house/play-1/"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			namespace, play, kind, ok := parsePlayTopic(base, each.topic)
			if ok != each.ok {
				t.Fatalf("ok = %v, want %v", ok, each.ok)
			}
			if namespace != each.namespace || play != each.play || kind != each.kind {
				t.Errorf("parsed (%q, %q, %q), want (%q, %q, %q)",
					namespace, play, kind, each.namespace, each.play, each.kind)
			}
		})
	}
}

// The payload is the contract between the jellyfin role and the progress
// role, so the field names are read here off the JSON both of them speak.
func TestAnOutsidePlayReadsItsFields(t *testing.T) {
	payload := []byte(`{"player":"jellyfin","people":["chris"],` +
		`"aliases":{"tmdb":"603","imdb":"tt0133093"},"season":0,"episode":0,` +
		`"position":4210,"duration":8160,"ended":false,"at":1757300000}`)

	outside := outsidePlay{}
	if err := json.Unmarshal(payload, &outside); err != nil {
		t.Fatal(err)
	}

	if outside.Player != "jellyfin" || len(outside.People) != 1 || outside.People[0] != "chris" {
		t.Errorf("outside = %+v, want the player and the person", outside)
	}
	if outside.Aliases["tmdb"] != "603" || outside.Aliases["imdb"] != "tt0133093" {
		t.Errorf("aliases = %v, want the two provider ids", outside.Aliases)
	}
	if outside.Position != 4210 || outside.Duration != 8160 || outside.At != 1757300000 {
		t.Errorf("outside = %+v, want the position, the duration, and the time", outside)
	}
	if outside.Ended {
		t.Errorf("ended = %v, want a play still running", outside.Ended)
	}
}

// A person is cluster-scoped, so a forget request names no namespace and an
// answer names one.
func TestParsePersonTopicNamesThePersonAndTheKind(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name      string
		topic     string
		person    string
		kind      string
		namespace string
		ok        bool
	}{
		{
			name:   "a forget request",
			topic:  personForgetTopic(base, "thora"),
			person: "thora",
			kind:   personForgetKind,
			ok:     true,
		},
		{
			name:      "one namespace's answer",
			topic:     personForgottenTopic(base, "thora", "house"),
			person:    "thora",
			kind:      personForgottenKind,
			namespace: "house",
			ok:        true,
		},
		{name: "a topic under another base", topic: "other/people/thora/forget"},
		{name: "a plays topic", topic: playOutsideTopic(base, "house", "play-1")},
		{name: "a people topic with a kind this operator does not read", topic: base + "/people/thora/watched"},
		{name: "an answer with no namespace", topic: base + "/people/thora/forgotten"},
		{name: "an empty person", topic: base + "/people//forget"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			person, kind, namespace, ok := parsePersonTopic(base, each.topic)
			if ok != each.ok {
				t.Fatalf("ok = %v, want %v", ok, each.ok)
			}
			if person != each.person || kind != each.kind || namespace != each.namespace {
				t.Errorf("parsed (%q, %q, %q), want (%q, %q, %q)",
					person, kind, namespace, each.person, each.kind, each.namespace)
			}
		})
	}
}
