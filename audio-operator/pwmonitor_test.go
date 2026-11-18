package main

// The fixture pipewire/pw-dump-monitor.json has three JSON arrays one
// after another, as pw-dump -m prints them. The third removes a node.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Each graph that decodeGraphs delivers also has the objects of earlier
// arrays that the later arrays did not change.
func TestDecodeGraphsFollowsEveryBatch(t *testing.T) {
	var graphs []pwGraph
	err := decodeGraphs(bytes.NewReader(fixture(t, "pipewire/pw-dump-monitor.json")),
		func(graph pwGraph) error {
			graphs = append(graphs, graph)
			return nil
		})
	if err != nil {
		t.Fatalf("decoding the fixture's stream: %v", err)
	}
	if len(graphs) != 3 {
		t.Fatalf("delivered %d graphs, want three", len(graphs))
	}

	// The first array is the whole graph: one declared sink and no
	// speaker.
	first := graphs[0]
	if got := sinkNames(first)[pcmAddress{Card: 0, PCM: 0}]; got != "liken.audio.card0-pcm0" {
		t.Errorf("the first batch's sink = %q, want liken.audio.card0-pcm0", got)
	}
	if len(first.Speakers) != 0 {
		t.Errorf("the first batch has the speakers %v, want none", first.Speakers)
	}

	// The second array adds the speaker's device and its node.
	second := graphs[1]
	if len(sinkNames(second)) != 1 {
		t.Errorf("the second batch's outputs = %v, want the one the first named", sinkNames(second))
	}
	speaker := second.Speakers[testSpeakerAddress]
	if speaker.Node != testSpeakerNode {
		t.Fatalf("the second batch's speaker = %+v, want the node %s", speaker, testSpeakerNode)
	}
	if speaker.Device != 62 {
		t.Errorf("the speaker's device = %d, want 62", speaker.Device)
	}
	if speaker.Route == nil || speaker.Route.Index != 1 {
		t.Errorf("the speaker's route = %+v, want route index 1", speaker.Route)
	}

	// The third array removes the speaker's node and changes the
	// sink's levels. The device object stays, but a device with no node
	// is not a speaker.
	third := graphs[2]
	if len(third.Speakers) != 0 {
		t.Errorf("the removed node left the speakers %v, want none", third.Speakers)
	}
	node := third.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}]
	if want := []float64{0.25, 0.25}; !reflect.DeepEqual(node.Volumes, want) {
		t.Errorf("the sink's levels = %v, want %v", node.Volumes, want)
	}
	if !node.Mute {
		t.Error("the sink reads as unmuted after a batch that muted it, want muted")
	}
	if node.Format.Rate != 48000 {
		t.Errorf("the sink's rate = %d, want 48000", node.Format.Rate)
	}
}

// watchGraph's deliver returns an error when its context is canceled.
func TestDecodeGraphsStopsWhenTheDeliveryFails(t *testing.T) {
	stopped := errors.New("the reconciler went away")
	delivered := 0
	err := decodeGraphs(bytes.NewReader(fixture(t, "pipewire/pw-dump-monitor.json")),
		func(pwGraph) error {
			delivered++
			return stopped
		})
	if !errors.Is(err, stopped) {
		t.Fatalf("decodeGraphs = %v, want the delivery's own error", err)
	}
	if delivered != 1 {
		t.Errorf("delivered %d graphs, want 1, because the first delivery failed", delivered)
	}
}

func TestDecodeGraphsReportsABrokenStream(t *testing.T) {
	err := decodeGraphs(bytes.NewReader([]byte(`[{"id": 1, "info": {}}`)), func(pwGraph) error { return nil })
	if err == nil {
		t.Fatal("decodeGraphs of a truncated array returned no error, want one")
	}
}

func TestMonitorErrorNamesTheReason(t *testing.T) {
	decoded := errors.New("reading pw-dump -m's output: unexpected EOF")
	exit := errors.New("signal: killed")
	cases := []struct {
		name       string
		decoded    error
		exit       error
		complaints string
		want       string
	}{
		{name: "the decode failed", decoded: decoded, exit: exit, want: decoded.Error()},
		{
			name:       "the process died with something to say",
			exit:       exit,
			complaints: "can't connect: Host is down\n",
			want:       "pw-dump -m stopped: signal: killed: can't connect: Host is down",
		},
		{name: "the process died silently", exit: exit, want: "pw-dump -m stopped: signal: killed"},
		{
			name:       "the process ended cleanly with something to say",
			complaints: "remote error\n",
			want:       "pw-dump -m stopped: remote error",
		},
		{name: "the process ended cleanly and silently", want: "pw-dump -m stopped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := monitorError(c.decoded, c.exit, c.complaints)
			if got.Error() != c.want {
				t.Errorf("monitorError = %q, want %q", got, c.want)
			}
		})
	}
}

// stubPWDump puts a script named pw-dump first on the PATH. It prints
// the stream, writes a line to stderr, and exits with status 3, as
// pw-dump -m does when PipeWire stops.
func stubPWDump(t *testing.T, stream []byte) {
	t.Helper()
	dir := t.TempDir()
	printed := filepath.Join(dir, "stream.json")
	if err := os.WriteFile(printed, stream, 0o644); err != nil {
		t.Fatalf("writing the stream the stub prints: %v", err)
	}
	script := "#!/bin/sh\ncat " + printed + "\necho 'remote error: Connection reset' >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "pw-dump"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the pw-dump stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestWatchGraphDeliversEveryBatchAndReportsTheEnd(t *testing.T) {
	stubPWDump(t, fixture(t, "pipewire/pw-dump-monitor.json"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	monitor, err := watchGraph(ctx)
	if err != nil {
		t.Fatalf("starting the monitor: %v", err)
	}
	var graphs []pwGraph
	for graph := range monitor.Graphs() {
		graphs = append(graphs, graph)
	}
	if len(graphs) != 3 {
		t.Fatalf("delivered %d graphs, want three", len(graphs))
	}
	if got := graphs[1].Speakers[testSpeakerAddress].Node; got != testSpeakerNode {
		t.Errorf("the second batch's speaker = %q, want %q", got, testSpeakerNode)
	}
	want := "pw-dump -m stopped: exit status 3: remote error: Connection reset"
	if got := monitor.Err(); got == nil || got.Error() != want {
		t.Errorf("Err = %v, want %q", got, want)
	}
}

func TestWatchGraphStopsWithItsContext(t *testing.T) {
	stubPWDump(t, fixture(t, "pipewire/pw-dump-monitor.json"))
	ctx, cancel := context.WithCancel(context.Background())

	monitor, err := watchGraph(ctx)
	if err != nil {
		t.Fatalf("starting the monitor: %v", err)
	}
	<-monitor.Graphs()
	cancel()
	for range monitor.Graphs() {
	}
	if monitor.Err() == nil {
		t.Error("a cancelled monitor's Err is nil, want the reason")
	}
}

func TestGraphFeedFallsBackToAPlainReadUntilAGraphArrives(t *testing.T) {
	polls := 0
	feed := &graphFeed{poll: func(context.Context) (pwGraph, error) {
		polls++
		return outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: "polled"}), nil
	}}

	graph, err := feed.read(context.Background())
	if err != nil {
		t.Fatalf("reading the feed: %v", err)
	}
	if got := sinkNames(graph)[pcmAddress{Card: 0, PCM: 0}]; got != "polled" || polls != 1 {
		t.Fatalf("the first read returned %q after %d polls, want polled after 1 poll", got, polls)
	}

	feed.deliver(outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: "delivered"}))
	graph, err = feed.read(context.Background())
	if err != nil {
		t.Fatalf("reading the feed: %v", err)
	}
	if got := sinkNames(graph)[pcmAddress{Card: 0, PCM: 0}]; got != "delivered" || polls != 1 {
		t.Fatalf("the read after a delivered graph returned %q after %d polls, want delivered after 1 poll", got, polls)
	}

	feed.monitorEnded()
	if _, err := feed.read(context.Background()); err != nil {
		t.Fatalf("reading the feed: %v", err)
	}
	if polls != 2 {
		t.Errorf("polls = %d, want 2, because the read after the monitor ended polls", polls)
	}
}

func TestGraphFeedReportsAFailedRead(t *testing.T) {
	feed := &graphFeed{poll: failingGraph}
	if _, err := feed.read(context.Background()); err == nil {
		t.Fatal("reading the feed of a PipeWire that does not respond returned no error, want one")
	}
}

// After the channel from changes closes, the next call to changes
// returns a new, open channel.
func TestTheFeedSignalsEveryChange(t *testing.T) {
	cases := []struct {
		name   string
		change func(*graphFeed)
	}{
		{"a delivered graph", func(f *graphFeed) { f.deliver(pwGraph{}) }},
		{"a monitor that ended", func(f *graphFeed) { f.monitorEnded() }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			feed := &graphFeed{poll: failingGraph}
			changed := feed.changes()
			select {
			case <-changed:
				t.Fatal("the signal fired before anything changed, want no signal")
			default:
			}
			c.change(feed)
			select {
			case <-changed:
			default:
				t.Fatal("the signal is open after the change, want it closed")
			}
			select {
			case <-feed.changes():
				t.Error("the next signal fired before the next change, want no signal")
			default:
			}
		})
	}
}

// await reads the graph again only when the feed delivers one, and no
// timer starts a read.
func TestACodecWaitReadsAgainWhenTheFeedDelivers(t *testing.T) {
	before := currentSink()
	after := currentSink()
	after.Codec, after.NodeID = "aptx", 99

	polled := make(chan struct{}, 1)
	feed := &graphFeed{poll: func(context.Context) (pwGraph, error) {
		polled <- struct{}{}
		return speakerGraph(map[string]bluezSink{testSpeakerAddress: before}), nil
	}}
	change := codecSwitch{read: feed.read, changes: feed.changes, timeout: 10 * time.Second}

	type answer struct {
		sink bluezSink
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		sink, err := change.await(context.Background(), testSpeakerAddress, "aptx")
		done <- answer{sink, err}
	}()
	<-polled
	feed.deliver(speakerGraph(map[string]bluezSink{testSpeakerAddress: after}))

	got := <-done
	if got.err != nil {
		t.Fatalf("waiting for the codec switch: %v", got.err)
	}
	if got.sink.NodeID != 99 {
		t.Errorf("the wait returned %+v, want the node 99", got.sink)
	}
	if extra := len(polled); extra != 0 {
		t.Errorf("the wait read PipeWire itself %d more times, want none", extra)
	}
}
