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
		{name: "remote availability", got: remoteAvailabilityTopic(base, "house", "sofa"), want: "liken/media/remotes/house/sofa/availability"},
		{name: "remote availability filter", got: remoteAvailabilityFilter(base), want: "liken/media/remotes/+/+/availability"},
		{name: "remote codes filter", got: remoteCodesFilter(base), want: "liken/media/remotes/+/+/codes"},
		{name: "player status", got: playerStatusTopic(base, "house", "theater"), want: "liken/media/players/house/theater/status"},
		{name: "focus filter", got: remoteFocusFilter(base), want: "liken/media/remotes/+/+/focus"},
		{name: "focus cycle filter", got: remoteFocusCycleFilter(base), want: "liken/media/remotes/+/+/focus/cycle"},
		{name: "play status", got: playStatusTopic(base, "house", "movie"), want: "liken/media/plays/house/movie/status"},
		{name: "play availability", got: playAvailabilityTopic(base, "house", "movie"), want: "liken/media/plays/house/movie/availability"},
		{name: "play commands", got: playCommandsTopic(base, "house", "movie"), want: "liken/media/plays/house/movie/commands"},
		{name: "player commands", got: playerCommandsTopic(base, "house", "theater"), want: "liken/media/players/house/theater/commands"},
		{name: "player panel", got: playerPanelTopic(base, "house", "theater"), want: "liken/media/players/house/theater/panel"},
		{name: "player panel filter", got: playerPanelFilter(base), want: "liken/media/players/+/+/panel"},
		{name: "player volume", got: playerVolumeTopic(base, "house", "theater"), want: "liken/media/players/house/theater/volume"},
		{name: "player volume filter", got: playerVolumeFilter(base), want: "liken/media/players/+/+/volume"},
		{name: "keys", got: remoteKeysTopic(base, "house", "sofa"), want: "liken/media/remotes/house/sofa/keys"},
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

// The availability parser matches only an availability topic, and the
// codes parser only a codes topic. Each of the remotes parsers keys on its
// own kind, so the operator folds each message down one path.
func TestTheRemoteParsersMatchTheirOwnTopicAlone(t *testing.T) {
	base := defaultTopicBase
	availability := remoteAvailabilityTopic(base, "house", "sofa")
	codes := remoteCodesTopic(base, "house", "sofa")

	cases := []struct {
		name      string
		parse     func(base, topic string) (string, string, bool)
		topic     string
		namespace string
		remote    string
		ok        bool
	}{
		{name: "availability", parse: parseRemoteAvailabilityTopic, topic: availability, namespace: "house", remote: "sofa", ok: true},
		{name: "availability against codes", parse: parseRemoteAvailabilityTopic, topic: codes},
		{name: "availability against focus", parse: parseRemoteAvailabilityTopic, topic: remoteFocusTopic(base, "house", "sofa")},
		{name: "availability against the cycle topic", parse: parseRemoteAvailabilityTopic, topic: remoteFocusCycleTopic(base, "house", "sofa")},
		{name: "availability against a play", parse: parseRemoteAvailabilityTopic, topic: playAvailabilityTopic(base, "house", "movie")},
		{name: "availability under another base", parse: parseRemoteAvailabilityTopic, topic: "other/remotes/house/sofa/availability"},
		{name: "codes", parse: parseRemoteCodesTopic, topic: codes, namespace: "house", remote: "sofa", ok: true},
		{name: "codes against availability", parse: parseRemoteCodesTopic, topic: availability},
		{name: "an empty remote name", parse: parseRemoteCodesTopic, topic: base + "/remotes/house//codes"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			namespace, remote, ok := each.parse(base, each.topic)
			mustMatch(t, ok, each.ok)
			mustMatch(t, namespace, each.namespace)
			mustMatch(t, remote, each.remote)
		})
	}
}

// The panel topic maps back to the unit whose sidecar published it,
// and no other players topic reads as one.
func TestParsePlayerPanelTopicNamesThePlayer(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name      string
		topic     string
		namespace string
		player    string
		ok        bool
	}{
		{
			name:      "a panel topic",
			topic:     playerPanelTopic(base, "house", "theater"),
			namespace: "house",
			player:    "theater",
			ok:        true,
		},
		{name: "the status topic", topic: playerStatusTopic(base, "house", "theater")},
		{name: "the commands topic", topic: playerCommandsTopic(base, "house", "theater")},
		{name: "the volume topic", topic: playerVolumeTopic(base, "house", "theater")},
		{name: "another tree", topic: remoteCodesTopic(base, "house", "sofa")},
		{name: "an empty namespace", topic: base + "/players//theater/panel"},
		{name: "an empty player name", topic: base + "/players/house//panel"},
		{name: "a segment too many", topic: base + "/players/house/theater/panel/state"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			namespace, player, ok := parsePlayerPanelTopic(base, each.topic)
			mustMatch(t, ok, each.ok)
			mustMatch(t, namespace, each.namespace)
			mustMatch(t, player, each.player)
		})
	}
}

// The volume topic maps back to the unit whose level it carries,
// and no other players topic reads as one.
func TestParsePlayerVolumeTopicNamesThePlayer(t *testing.T) {
	base := defaultTopicBase
	cases := []struct {
		name      string
		topic     string
		namespace string
		player    string
		ok        bool
	}{
		{
			name:      "a volume topic",
			topic:     playerVolumeTopic(base, "house", "theater"),
			namespace: "house",
			player:    "theater",
			ok:        true,
		},
		{name: "the panel topic", topic: playerPanelTopic(base, "house", "theater")},
		{name: "the status topic", topic: playerStatusTopic(base, "house", "theater")},
		{name: "another base", topic: "other/players/house/theater/volume"},
		{name: "an empty namespace", topic: base + "/players//theater/volume"},
		{name: "an empty player name", topic: base + "/players/house//volume"},
		{name: "a segment too many", topic: base + "/players/house/theater/volume/level"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			namespace, player, ok := parsePlayerVolumeTopic(base, each.topic)
			mustMatch(t, ok, each.ok)
			mustMatch(t, namespace, each.namespace)
			mustMatch(t, player, each.player)
		})
	}
}
