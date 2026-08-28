package main

// The graph feed, against a fixture of three arrays in one file, the
// shape pw-dump -m prints. The third array holds a removal.

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

// What each batch of the fixture says, and that the
// graph after each one is built from every object the stream has
// named so far.
func TestDecodeGraphsFollowsEveryBatch(t *testing.T) {
	var graphs []pwGraph
	err := decodeGraphs(bytes.NewReader(fixture(t, "pipewire/pw-dump-monitor.json")),
		func(graph pwGraph) error {
			graphs = append(graphs, graph)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 3 {
		t.Fatalf("delivered %d graphs, want three", len(graphs))
	}

	// The first array is the whole graph: one declared sink and no
	// speaker.
	first := graphs[0]
	if got := sinkNames(first)[pcmAddress{Card: 0, PCM: 0}]; got != "liken.audio.card0-pcm0" {
		t.Errorf("the first batch's sink = %q", got)
	}
	if len(first.Speakers) != 0 {
		t.Errorf("the first batch holds the speakers %v", first.Speakers)
	}

	// The second array adds the speaker's device and its node, and
	// the sink the first array named is still there.
	second := graphs[1]
	if len(sinkNames(second)) != 1 {
		t.Errorf("the second batch's outputs = %v, want the one the first named", sinkNames(second))
	}
	speaker := second.Speakers[testSpeakerAddress]
	if speaker.Node != testSpeakerNode {
		t.Fatalf("the second batch's speaker = %+v", speaker)
	}
	if speaker.Device != 62 {
		t.Errorf("the speaker's device = %d, want 62", speaker.Device)
	}
	if speaker.Route == nil || speaker.Route.Index != 1 {
		t.Errorf("the speaker's route = %+v", speaker.Route)
	}

	// The third array removes the speaker's node and changes the
	// sink's levels. The device object stays, and a device with no
	// node of its own is no speaker.
	third := graphs[2]
	if len(third.Speakers) != 0 {
		t.Errorf("the removed node left the speakers %v", third.Speakers)
	}
	node := third.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}]
	if want := []float64{0.25, 0.25}; !reflect.DeepEqual(node.Volumes, want) {
		t.Errorf("the sink's levels = %v, want %v", node.Volumes, want)
	}
	if !node.Mute {
		t.Error("the sink reports itself unmuted after a batch that muted it")
	}
	if node.Format.Rate != 48000 {
		t.Errorf("the sink's rate = %d, want 48000", node.Format.Rate)
	}
}

// A deliver that fails stops the stream, which is
// what a cancelled context does to the monitor.
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
		t.Errorf("delivered %d graphs after the first one failed", delivered)
	}
}

// A stream that ends mid-array is a broken
// pw-dump and not the end of the graph.
func TestDecodeGraphsReportsABrokenStream(t *testing.T) {
	err := decodeGraphs(bytes.NewReader([]byte(`[{"id": 1, "info": {}}`)), func(pwGraph) error { return nil })
	if err == nil {
		t.Fatal("a truncated array did not report an error")
	}
}

// Pw-dump -m ending is the reconciler's signal to
// start it again, and that the reason names which end it was.
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
				t.Errorf("error = %q, want %q", got, c.want)
			}
		})
	}
}

// The stand-in is a real process on the path
// printing the fixture and then failing, so the test drives the exec,
// the pipe, and the exit the way a PipeWire that went away would.
func stubPWDump(t *testing.T, stream []byte) {
	t.Helper()
	dir := t.TempDir()
	printed := filepath.Join(dir, "stream.json")
	if err := os.WriteFile(printed, stream, 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat " + printed + "\necho 'remote error: Connection reset' >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "pw-dump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// One pw-dump -m runs for the pod's life, that a
// graph arrives on the channel after every batch, and that a stream
// that ends closes the channel with the reason in Err, which is what
// the reconciler logs before it starts the monitor again.
func TestWatchGraphDeliversEveryBatchAndReportsTheEnd(t *testing.T) {
	stubPWDump(t, fixture(t, "pipewire/pw-dump-monitor.json"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	monitor, err := watchGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var graphs []pwGraph
	for graph := range monitor.Graphs() {
		graphs = append(graphs, graph)
	}
	if len(graphs) != 3 {
		t.Fatalf("delivered %d graphs, want three", len(graphs))
	}
	if got := graphs[1].Speakers[testSpeakerAddress].Node; got != testSpeakerNode {
		t.Errorf("the second batch's speaker = %q", got)
	}
	want := "pw-dump -m stopped: exit status 3: remote error: Connection reset"
	if got := monitor.Err(); got == nil || got.Error() != want {
		t.Errorf("Err = %v, want %q", got, want)
	}
}

// A cancelled context ends the monitor, which is
// how the pod's shutdown reaches the one long-lived process.
func TestWatchGraphStopsWithItsContext(t *testing.T) {
	stubPWDump(t, fixture(t, "pipewire/pw-dump-monitor.json"))
	ctx, cancel := context.WithCancel(context.Background())

	monitor, err := watchGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	<-monitor.Graphs()
	cancel()
	for range monitor.Graphs() {
	}
	if monitor.Err() == nil {
		t.Error("a cancelled monitor reported no reason")
	}
}

// The feed answers every pass from the graph the monitor last
// delivered, so a pass costs no process. Before the first batch, and
// while the monitor is starting again, a pass reads PipeWire itself.
func TestGraphFeedFallsBackToAPlainReadUntilAGraphArrives(t *testing.T) {
	polls := 0
	feed := &graphFeed{poll: func(context.Context) (pwGraph, error) {
		polls++
		return outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: "polled"}), nil
	}}

	graph, err := feed.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := sinkNames(graph)[pcmAddress{Card: 0, PCM: 0}]; got != "polled" || polls != 1 {
		t.Fatalf("the first read gave %q after %d polls", got, polls)
	}

	feed.deliver(outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: "delivered"}))
	graph, err = feed.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := sinkNames(graph)[pcmAddress{Card: 0, PCM: 0}]; got != "delivered" || polls != 1 {
		t.Fatalf("a delivered graph gave %q after %d polls", got, polls)
	}

	// A monitor that ended leaves a graph that stopped moving, so the
	// pass reads PipeWire again rather than answering from it.
	feed.lost()
	if _, err := feed.read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if polls != 2 {
		t.Errorf("polls = %d, want the read after the monitor was lost", polls)
	}
}

// A PipeWire that is gone fails the read, which is the failure the
// reconcile pass counts toward the restart.
func TestGraphFeedReportsAFailedRead(t *testing.T) {
	feed := &graphFeed{poll: failingSinks}
	if _, err := feed.read(context.Background()); err == nil {
		t.Fatal("a PipeWire that does not answer read without an error")
	}
}
