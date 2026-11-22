package main

import (
	"strings"
	"testing"
)

// passthroughCluster is volumeCluster with spec.passthrough On for the
// Player theater, whose Sink front reports every format in
// spdifFormats.
func passthroughCluster() *fakeCluster {
	cluster := volumeCluster()
	cluster.players["theater"].Spec.Passthrough = passthroughOn
	cluster.sinks["front"].Status.Passthrough = []string{"AC3", "EAC3", "DTS", "DTS-HD", "TrueHD"}
	return cluster
}

func TestSinkFormatsMapToMpvCodecsInFixedOrder(t *testing.T) {
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

func TestPassthroughDecision(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fakeCluster)
		codecs string
		reason string
	}{
		{name: "all conditions met", change: func(*fakeCluster) {},
			codecs: "ac3,eac3,dts,dts-hd,truehd"},
		{name: "passthrough unset", change: func(c *fakeCluster) { c.players["theater"].Spec.Passthrough = "" }},
		{name: "passthrough Off", change: func(c *fakeCluster) { c.players["theater"].Spec.Passthrough = passthroughOff }},
		{name: "two sinks", change: func(c *fakeCluster) {
			c.players["theater"].Spec.Sinks = append(c.players["theater"].Spec.Sinks, PlayerDevice{Class: "audio-speaker"})
		}, reason: "spec.sinks has 2 entries, and passthrough needs exactly one"},
		{name: "receiver unreachable", change: func(c *fakeCluster) {
			c.receivers["den-receiver"].Status.Conditions = []ReceiverCondition{{Type: receiverReachableCondition, Status: "False"}}
		}, reason: "no Receiver matches the Player's screen with a Reachable condition of True, and passthrough needs one to set the volume"},
		{name: "no receiver", change: func(c *fakeCluster) { c.receiversAbsent = true },
			reason: "no Receiver matches the Player's screen with a Reachable condition of True, and passthrough needs one to set the volume"},
		{name: "no sink in status.sinks", change: func(c *fakeCluster) { c.players["theater"].Status.Sinks = nil },
			reason: "status.sinks lists no Sink, and stays empty until the Player's first playback claim is allocated"},
		{name: "sink missing from the operator view", change: func(c *fakeCluster) { delete(c.sinks, "front") },
			reason: "Sink front does not exist"},
		{name: "sink fails to convert", change: func(c *fakeCluster) { c.fails[sinksPath+"/front"] = true },
			reason: "reading Sink front: Garbled front does not convert: cannot restore struct from: string"},
		{name: "sink accepts nothing", change: func(c *fakeCluster) { c.sinks["front"].Status.Passthrough = nil },
			reason: "Sink front lists no encoded format in status.passthrough"},
		{name: "sink accepts only unknown names", change: func(c *fakeCluster) { c.sinks["front"].Status.Passthrough = []string{"PCM"} },
			reason: "Sink front lists no encoded format in status.passthrough"},
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

func TestPassthroughOptionReachesThePlayer(t *testing.T) {
	cases := []struct {
		name   string
		prefs  resolvedPreferences
		codecs []string
		want   string
	}{
		{name: "alone", codecs: []string{"ac3", "eac3"}, want: houseStyleLines + "\n--audio-spdif=ac3,eac3"},
		{name: "after the preferences", prefs: resolvedPreferences{AudioLanguages: []string{"en"}}, codecs: []string{"truehd"},
			want: "--alang=en\n--subs-match-os-language=no\n" + houseStyleLines + "\n--audio-spdif=truehd"},
		{name: "no codecs and no preferences", want: houseStyleLines},
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

func TestPlayStartLogsPassthroughDecision(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fakeCluster)
		want   []string
		option string
	}{
		{name: "passes through", change: func(*fakeCluster) {}, want: []string{
			"play house/movie: mpv passes ac3,eac3,dts,dts-hd,truehd through to sink front, because Player theater sets spec.passthrough to On",
		}, option: houseStyleLines + "\n--audio-spdif=ac3,eac3,dts,dts-hd,truehd"},
		{name: "decodes", change: func(c *fakeCluster) { c.sinks["front"].Status.Passthrough = nil }, want: []string{
			"play house/movie: mpv decodes every track, although Player theater sets spec.passthrough to On, because Sink front lists no encoded format in status.passthrough",
		}, option: houseStyleLines},
		{name: "passthrough unset", change: func(c *fakeCluster) { c.players["theater"].Spec.Passthrough = "" }, option: houseStyleLines},
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

func passthroughLines(log *logBuffer) []string {
	var kept []string
	for _, line := range linesAbout(log, "play house/movie") {
		if strings.Contains(line, "passthrough") {
			kept = append(kept, line)
		}
	}
	return kept
}
