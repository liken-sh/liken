package main

// imagesubtitleread_test.go proves the read of ffprobe's packet output as it
// arrives: the packets it gives, the bytes of each packet's dump, and the end
// of a read that the probe stops.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// ffprobe's answer to a window read with -of json=c=1, which writes each
// packet on one line.
const ffprobePacketAnswer = `{
    "packets": [
        { "codec_type": "subtitle", "stream_index": 3, "pts_time": "1180.429000", "data": "\n00000000: 1600 1234                                ...4\n" },
        { "codec_type": "subtitle", "stream_index": 2, "pts_time": "1181.000000", "data": "\n00000000: 4120 6c69 6e65 2e                        A line.\n" },
        { "codec_type": "subtitle", "stream_index": 3, "pts_time": "1182.500000", "data": "\n00000000: 80                                       .\n" }
    ]
}
`

func readAll(t *testing.T, answer io.Reader, stopAfter int) ([]subtitlePacket, bool, error) {
	t.Helper()
	var packets []subtitlePacket
	stopped, err := readSubtitlePackets(answer, func(packet subtitlePacket) bool {
		packets = append(packets, packet)
		return len(packets) < stopAfter
	})
	return packets, stopped, err
}

func TestTheReadGivesEachPacketAsItArrives(t *testing.T) {
	packets, stopped, err := readAll(t, strings.NewReader(ffprobePacketAnswer), 10)

	if err != nil || stopped {
		t.Fatalf("stopped %v, error %v, want the whole answer read", stopped, err)
	}
	var streams []int
	for _, packet := range packets {
		streams = append(streams, packet.Stream)
	}
	if !slices.Equal(streams, []int{3, 2, 3}) || packets[0].Time != "1180.429000" {
		t.Errorf("packets = %+v, want streams 3, 2, 3 from 1180.429", packets)
	}
}

// The probe stops reading at the packet it needs no more after, so the rest
// of the answer, which ffprobe never finishes, is never read.
func TestAStoppedReadReadsNoFurther(t *testing.T) {
	cut := ffprobePacketAnswer[:strings.Index(ffprobePacketAnswer, `"pts_time": "1182`)]

	packets, stopped, err := readAll(t, strings.NewReader(cut), 2)

	if err != nil || !stopped || len(packets) != 2 {
		t.Errorf("%d packets, stopped %v, error %v, want 2 packets and a stop", len(packets), stopped, err)
	}
}

func TestTheEndsOfAnAnswer(t *testing.T) {
	cases := []struct {
		name    string
		answer  string
		packets int
		failed  bool
	}{
		{name: "a window with no packet", answer: "{\n\n}\n"},
		{name: "an empty packet list", answer: `{"packets": []}`},
		{name: "no output", answer: ""},
		{name: "an answer cut in a packet", answer: ffprobePacketAnswer[:200], packets: 1, failed: true},
		{name: "an answer that is not JSON", answer: "Invalid data found when processing input", failed: true},
		{name: "a packet list that is not a list", answer: `{"packets": {}}`, failed: true},
		{name: "an answer cut after the name of the list", answer: `{"packets"`, failed: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			packets, _, err := readAll(t, strings.NewReader(test.answer), 10)

			if len(packets) != test.packets || (err != nil) != test.failed {
				t.Errorf("%d packets, error %v, want %d packets and an error %v", len(packets), err, test.packets, test.failed)
			}
		})
	}
}

// The dump of a packet reads back to its bytes at every length of its last
// line, and the text column, which can contain any character, is not read.
func TestAPacketDumpReadsBackToItsBytes(t *testing.T) {
	for _, length := range []int{0, 1, 2, 15, 16, 17, 33} {
		data := bytes.Repeat([]byte{0x3a, 0x20, 0x7f, 0x16}, 9)[:length]

		got, err := packetBytes(hexDump(data))

		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("packetBytes of %d bytes = %x, %v, want %x", length, got, err, data)
		}
	}
}

func TestADumpThatIsNotHexDoesNotRead(t *testing.T) {
	if _, err := packetBytes("\n00000000: 16zz                                     ..\n"); err == nil {
		t.Error("packetBytes read a dump that is not hex, want an error")
	}
}

// The read asks ffprobe for the packets and their bytes of every subtitle
// stream, from the window's start for 300 seconds.
func TestTheWindowReadAsksForEverySubtitleStreamFor300Seconds(t *testing.T) {
	got := subtitleWindowArguments("/library/a.mkv", 1234.5)

	want := []string{"-v", "error", "-print_format", "json=c=1", "-select_streams", "s",
		"-read_intervals", "1234.5%+300", "-show_packets", "-show_data",
		"-show_entries", "packet=stream_index,pts_time,data", "/library/a.mkv"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("arguments = %q, want %q", got, want)
	}
}

// A window that the probe stopped, and one that the limit ended, count with
// the packets that arrived, whatever ffprobe's exit says. Any other read
// fails with its first cause.
func TestSubtitleWindowResult(t *testing.T) {
	parse := errors.New("unexpected end of JSON input")
	exit := errors.New("exit status 1")
	cases := []struct {
		name                string
		parent              error
		stopped, limitEnded bool
		readErr, waitErr    error
		words               string
		want                string
	}{
		{name: "ffprobe finished the window"},
		{name: "the probe stopped the read", stopped: true, waitErr: context.Canceled},
		{name: "the limit ended the read", limitEnded: true, readErr: parse, waitErr: context.DeadlineExceeded},
		{name: "the probe's own context ended", parent: context.Canceled, stopped: true, want: "context canceled"},
		{name: "the answer does not parse", readErr: parse, waitErr: context.Canceled,
			want: "reading the packets that ffprobe wrote: unexpected end of JSON input"},
		{name: "ffprobe failed", readErr: parse, waitErr: exit, words: "No such file or directory\n",
			want: "ffprobe: exit status 1: No such file or directory"},
		{name: "ffprobe failed and wrote nothing", waitErr: exit, want: "ffprobe: exit status 1"},
		{name: "the answer does not parse and ffprobe exited well", readErr: parse, words: "a warning",
			want: "reading the packets that ffprobe wrote: unexpected end of JSON input: a warning"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := subtitleWindowResult(tc.parent, tc.stopped, tc.limitEnded, tc.readErr, tc.waitErr, tc.words)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tc.want {
				t.Errorf("the result is %q, want %q", got, tc.want)
			}
		})
	}
}
