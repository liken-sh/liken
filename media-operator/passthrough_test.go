package main

// These tests cover the passthrough decision: which encoded formats mpv
// sends to the receiver unchanged, the option that carries them to the
// player container, and the line a play start writes for a Player that
// states passthrough On.

import (
	"strings"
	"testing"
)

// passthroughCluster is the house unit wired into a reachable Receiver,
// with the front Sink remembered on the Player. The Player states
// passthrough On, and the front Sink accepts every encoded format the
// audio operator reports.
func passthroughCluster() *fakeCluster {
	cluster := volumeCluster()
	cluster.players["theater"].Spec.Passthrough = passthroughOn
	cluster.sinks["front"].Status.Passthrough = []string{"AC3", "EAC3", "DTS", "DTS-HD", "TrueHD"}
	return cluster
}

// Each audio-operator name maps to its mpv name, in one fixed order
// whatever order the Sink lists them in, and a name that is not one of
// the five is dropped.
func TestTheSinksFormatsMapToMpvCodecs(t *testing.T) {
	cases := []struct {
		name    string
		formats []string
		want    string
	}{
		{name: "every format", formats: []string{"TrueHD", "DTS-HD", "DTS", "EAC3", "AC3"}, want: "ac3,eac3,dts,dts-hd,truehd"},
		{name: "one format", formats: []string{"AC3"}, want: "ac3"},
		{name: "an unknown name is dropped", formats: []string{"PCM", "EAC3", "MPEG"}, want: "eac3"},
		{name: "a name in another case is dropped", formats: []string{"ac3", "TRUEHD"}, want: ""},
		{name: "no formats", formats: nil, want: ""},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, strings.Join(spdifCodecs(each.formats), ","), each.want)
		})
	}
}

// mpv gets the Sink's codecs only when the Player asks for passthrough,
// plays through one sink, has its level set by a reachable Receiver, and
// remembers a Sink that accepts an encoded format. Otherwise the Player
// that asked hears why it decodes.
func TestThePassthroughDecision(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fakeCluster)
		codecs string
		reason string
	}{
		{name: "every condition holds", change: func(*fakeCluster) {},
			codecs: "ac3,eac3,dts,dts-hd,truehd"},
		{name: "passthrough unset", change: func(c *fakeCluster) { c.players["theater"].Spec.Passthrough = "" }},
		{name: "passthrough Off", change: func(c *fakeCluster) { c.players["theater"].Spec.Passthrough = passthroughOff }},
		{name: "two sinks", change: func(c *fakeCluster) {
			c.players["theater"].Spec.Sinks = append(c.players["theater"].Spec.Sinks, PlayerDevice{Class: "audio-speaker"})
		}, reason: "passthrough needs exactly one entry in spec.sinks, and the player states 2"},
		{name: "receiver unreachable", change: func(c *fakeCluster) {
			c.receivers["den-receiver"].Status.Conditions = []ReceiverCondition{{Type: receiverReachableCondition, Status: "False"}}
		}, reason: "no Receiver with a Reachable condition of True sets the unit's level"},
		{name: "no receiver", change: func(c *fakeCluster) { c.receiversAbsent = true },
			reason: "no Receiver with a Reachable condition of True sets the unit's level"},
		{name: "no remembered sink", change: func(c *fakeCluster) { c.players["theater"].Status.Sinks = nil },
			reason: "status.sinks names no Sink yet, which is the case until the player's first playback claim is allocated"},
		{name: "remembered sink not in the view", change: func(c *fakeCluster) { delete(c.sinks, "front") },
			reason: "the operator holds no Sink front"},
		{name: "remembered sink does not convert", change: func(c *fakeCluster) { c.fails[sinksPath+"/front"] = true },
			reason: "reading Sink front failed: Garbled front does not convert: cannot restore struct from: string"},
		{name: "sink accepts nothing", change: func(c *fakeCluster) { c.sinks["front"].Status.Passthrough = nil },
			reason: "sink front accepts no encoded format in status.passthrough"},
		{name: "sink accepts only unknown names", change: func(c *fakeCluster) { c.sinks["front"].Status.Passthrough = []string{"PCM"} },
			reason: "sink front accepts no encoded format in status.passthrough"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := passthroughCluster()
			each.change(cluster)
			media := testOperator(t, cluster, make(chan struct{}, 1))

			decision := media.passthroughFor(cluster.players["theater"])

			mustMatch(t, strings.Join(decision.codecs, ","), each.codecs)
			mustMatch(t, decision.reason, each.reason)
		})
	}
}

// The option travels in the player container's options variable, after
// the preference options when there are any and alone when there are
// none.
func TestThePassthroughOptionReachesThePlayer(t *testing.T) {
	cases := []struct {
		name   string
		prefs  resolvedPreferences
		codecs []string
		want   string
	}{
		{name: "alone", codecs: []string{"ac3", "eac3"}, want: "--audio-spdif=ac3,eac3"},
		{name: "after the preferences", prefs: resolvedPreferences{AudioLanguages: []string{"en"}}, codecs: []string{"truehd"},
			want: "--alang=en\n--subs-match-os-language=no\n--audio-spdif=truehd"},
		{name: "no codecs and no preferences", want: ""},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			play := testPlay()
			pod := buildPod(play, buildClaim(play, testPlayer()), testResolution(t), testPlayerImage, testSidecarImage,
				testDisplayImage, testBusAddress, testTopicBase, nil, each.prefs, each.codecs, "")

			mustMatch(t, envValue(pod.Spec.Containers[0], playerOptionsVariable), each.want)
		})
	}
}

// A play start on a Player that states On writes one line, either the
// codecs mpv passes through or why it decodes. A Player that states
// nothing writes no new line, and its pod carries no option.
func TestAPlayStartLogsThePassthroughDecision(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fakeCluster)
		want   []string
		option string
	}{
		{name: "passes through", change: func(*fakeCluster) {}, want: []string{
			"play house/movie: mpv passes ac3,eac3,dts,dts-hd,truehd through to sink front, because player theater states passthrough On",
		}, option: "--audio-spdif=ac3,eac3,dts,dts-hd,truehd"},
		{name: "decodes", change: func(c *fakeCluster) { c.sinks["front"].Status.Passthrough = nil }, want: []string{
			"play house/movie: mpv decodes every track, although player theater states passthrough On, because sink front accepts no encoded format in status.passthrough",
		}},
		{name: "not asked", change: func(c *fakeCluster) { c.players["theater"].Spec.Passthrough = "" }},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := passthroughCluster()
			each.change(cluster)
			cluster.plays["movie"] = housePlay("https://nas/film.mkv")
			media, log := loggingOperator(t, cluster)

			media.pass()

			mustMatchAll(t, passthroughLines(log), each.want)
			mustMatch(t, envValue(cluster.pods["movie-playback"].Spec.Containers[0], playerOptionsVariable), each.option)
		})
	}
}

// passthroughLines keeps the Play's lines about passthrough, past the
// lines about its pod and its phase.
func passthroughLines(log *logBuffer) []string {
	var kept []string
	for _, line := range linesAbout(log, "play house/movie") {
		if strings.Contains(line, "passthrough") {
			kept = append(kept, line)
		}
	}
	return kept
}
