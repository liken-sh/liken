package main

// imagesubtitles_test.go proves how the probe reads the image subtitle
// streams of a file: which windows it reads, when it stops the read, and
// which streams get numbers. A fake answers in ffprobe's place with packets
// that the tests encode.

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// The subtitle packets of a file as the window read gives them, by the
// start of each window. The fake records each window the probe asked for
// and how many packets it handed over before the probe ended the read.
type fakeWindows struct {
	windows   map[float64][]subtitlePacket
	starts    []float64
	delivered int
	err       error
	// An error for one window alone.
	failures map[float64]error
}

func (f *fakeWindows) read(_ context.Context, _ string, start float64, each func(subtitlePacket) bool) error {
	f.starts = append(f.starts, start)
	for _, packet := range f.windows[start] {
		f.delivered++
		if !each(packet) {
			return nil
		}
	}
	if failure := f.failures[start]; failure != nil {
		return failure
	}
	return f.err
}

// A film of 4000 seconds with a PGS stream at index 1, a SubRip stream at
// index 2, and a VobSub stream of 480 rows at index 3.
func filmWithImageSubtitles() probedFile {
	return probedFile{Duration: 4000, Streams: []probedStream{
		{Kind: fileTypeVideo, Codec: "h264"},
		{Kind: fileTypeSubtitle, Codec: "hdmv_pgs_subtitle"},
		{Kind: fileTypeSubtitle, Codec: "subrip"},
		{Kind: fileTypeSubtitle, Codec: "dvd_subtitle", Width: 720, Height: 480},
	}}
}

func packetAt(stream int, at float64, data []byte) subtitlePacket {
	return subtitlePacket{Stream: stream, Time: strconv.FormatFloat(at, 'f', 3, 64), Data: hexDump(data)}
}

// n PGS cues of one 60-row line at canvas row y, each a display set and
// then a clear, one second apart from the second at.
func pgsCues(stream, n, y int, at float64) []subtitlePacket {
	cue := pgsTestSet{objects: []pgsTestObject{{x: 760, y: y, picture: picture(400, 60, band{start: 0, end: 60, entry: 1})}}}
	var packets []subtitlePacket
	for i := range n {
		second := at + float64(2*i)
		packets = append(packets, packetAt(stream, second, cue.packet()), packetAt(stream, second+1, pgsClear()))
	}
	return packets
}

// n VobSub cues of one 30-row line at canvas row y, one second apart.
func vobsubCues(stream, n, y int, at float64) []subtitlePacket {
	cue := spuTest{x: 200, y: y, alpha: usualSPUAlpha, picture: picture(300, 30, band{start: 0, end: 30, entry: 1})}
	var packets []subtitlePacket
	for i := range n {
		packets = append(packets, packetAt(stream, at+float64(i), cue.packet()))
	}
	return packets
}

func inOrder(groups ...[]subtitlePacket) []subtitlePacket {
	var all []subtitlePacket
	for _, group := range groups {
		all = append(all, group...)
	}
	return all
}

func imagesOf(record probedFile) []*probedImage {
	images := make([]*probedImage, len(record.Streams))
	for i, stream := range record.Streams {
		images[i] = stream.Image
	}
	return images
}

func measureWith(t *testing.T, windows *fakeWindows, record probedFile) probedFile {
	t.Helper()
	if err := measureImageSubtitles(t.Context(), windows.read, "/library/a.mkv", &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestTheProbeMeasuresEachImageStreamInAWindowAtAQuarterOfTheDuration(t *testing.T) {
	text := subtitlePacket{Stream: 2, Time: "1000.500", Data: hexDump([]byte("A line of dialogue."))}
	windows := &fakeWindows{windows: map[float64][]subtitlePacket{
		1000: inOrder([]subtitlePacket{text}, pgsCues(1, 12, 900, 1000), vobsubCues(3, 12, 400, 1000)),
	}}

	record := measureWith(t, windows, filmWithImageSubtitles())

	want := []*probedImage{nil, {CanvasHeight: 1080, LineHeight: 60, InkBottom: 960}, nil,
		{CanvasHeight: 480, LineHeight: 30, InkBottom: 430}}
	if got := imagesOf(record); !reflect.DeepEqual(got, want) {
		t.Errorf("images = %+v, want %+v", got, want)
	}
	if !record.Measured || !reflect.DeepEqual(windows.starts, []float64{1000}) {
		t.Errorf("measured %v from windows %v, want measured from the window at 1000", record.Measured, windows.starts)
	}
}

// The probe stops ffprobe at the packet that gives the last image stream its
// 20th lower cue. An upper cue does not count toward the 20.
func TestTheReadStopsWhenEveryImageStreamHasTwentyLowerCues(t *testing.T) {
	cases := []struct {
		name    string
		packets []subtitlePacket
		want    int
	}{
		{
			// The 20th VobSub cue is the 20th packet, and the 20th PGS cue
			// ends at the clear that is the 40th packet.
			name:    "both streams reach 20",
			packets: inOrder(vobsubCues(3, 30, 400, 1000), pgsCues(1, 30, 900, 1000)),
			want:    30 + 40,
		},
		{
			name:    "ten upper cues before the lower ones",
			packets: inOrder(vobsubCues(3, 30, 400, 1000), pgsCues(1, 10, 100, 1000), pgsCues(1, 30, 900, 1100)),
			want:    30 + 20 + 40,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			windows := &fakeWindows{windows: map[float64][]subtitlePacket{1000: test.packets}}

			measureWith(t, windows, filmWithImageSubtitles())

			if windows.delivered != test.want {
				t.Errorf("the probe read %d packets, want %d", windows.delivered, test.want)
			}
		})
	}
}

// A stream with fewer than 10 lower cues in the first window gets a second
// window at half the duration, and the cues of both count. A stream that
// had enough in the first window is not read again: the VobSub cues of the
// second window are higher, and its numbers do not move.
func TestAStreamWithFewCuesGetsASecondWindowAtHalfTheDuration(t *testing.T) {
	windows := &fakeWindows{windows: map[float64][]subtitlePacket{
		1000: inOrder(pgsCues(1, 6, 900, 1000), vobsubCues(3, 12, 400, 1000)),
		2000: inOrder(pgsCues(1, 6, 910, 2000), vobsubCues(3, 30, 300, 2000)),
	}}

	record := measureWith(t, windows, filmWithImageSubtitles())

	want := []*probedImage{nil, {CanvasHeight: 1080, LineHeight: 60, InkBottom: 970}, nil,
		{CanvasHeight: 480, LineHeight: 30, InkBottom: 430}}
	if got := imagesOf(record); !reflect.DeepEqual(got, want) {
		t.Errorf("images = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(windows.starts, []float64{1000, 2000}) {
		t.Errorf("windows = %v, want 1000 and 2000", windows.starts)
	}
}

func TestAStreamWithTooFewCuesInBothWindowsHasNoNumbers(t *testing.T) {
	windows := &fakeWindows{windows: map[float64][]subtitlePacket{
		1000: inOrder(pgsCues(1, 4, 900, 1000), vobsubCues(3, 20, 400, 1000)),
		2000: pgsCues(1, 5, 900, 2000),
	}}

	record := measureWith(t, windows, filmWithImageSubtitles())

	if record.Streams[1].Image != nil || record.Streams[3].Image == nil || !record.Measured {
		t.Errorf("images = %+v, measured %v, want none for the PGS stream and a measured record",
			imagesOf(record), record.Measured)
	}
}

// On a file shorter than 1200 seconds the two windows overlap. A packet that
// the first window read is not counted again, so 5 cues and 4 new ones are
// too few.
func TestAPacketOfTheFirstWindowIsNotCountedTwice(t *testing.T) {
	record := probedFile{Duration: 1000, Streams: []probedStream{{Kind: fileTypeSubtitle, Codec: "dvd_subtitle", Height: 480}}}
	windows := &fakeWindows{windows: map[float64][]subtitlePacket{
		250: vobsubCues(0, 5, 400, 500),
		500: vobsubCues(0, 9, 400, 500),
	}}

	record = measureWith(t, windows, record)

	if record.Streams[0].Image != nil {
		t.Errorf("image = %+v, want none from 9 distinct cues", record.Streams[0].Image)
	}
}

func TestTheWindowsOfAFileWithNoImageStream(t *testing.T) {
	cases := []struct {
		name   string
		record probedFile
		want   []float64
	}{
		{name: "a film with only a text track", record: probedFile{Duration: 4000, Streams: []probedStream{
			{Kind: fileTypeVideo}, {Kind: fileTypeSubtitle, Codec: "subrip"},
		}}},
		{name: "a VobSub stream with no size", record: probedFile{Duration: 4000, Streams: []probedStream{
			{Kind: fileTypeSubtitle, Codec: "dvd_subtitle"},
		}}},
		{name: "a text subtitle file", record: probedFile{Path: "a.en.srt"}},
		{
			// With no duration, the one window starts at the start of the file.
			name:   "a file with no duration",
			record: probedFile{Streams: []probedStream{{Kind: fileTypeSubtitle, Codec: "hdmv_pgs_subtitle"}}},
			want:   []float64{0},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			windows := &fakeWindows{}

			record := measureWith(t, windows, test.record)

			if !reflect.DeepEqual(windows.starts, test.want) || !record.Measured {
				t.Errorf("windows = %v, measured %v, want %v and measured", windows.starts, record.Measured, test.want)
			}
		})
	}
}

// A read that fails leaves the record unmeasured, so the next probe run
// reads the file again.
func TestAFailedWindowReadLeavesTheRecordUnmeasured(t *testing.T) {
	windows := &fakeWindows{err: errors.New("ffprobe: exit status 1: Invalid data found when processing input")}
	record := filmWithImageSubtitles()

	err := measureImageSubtitles(t.Context(), windows.read, "/library/a.mkv", &record)

	if err == nil || record.Measured {
		t.Errorf("error = %v, measured %v, want an error and no measured flag", err, record.Measured)
	}
}

func TestAFailedSecondWindowLeavesTheRecordUnmeasured(t *testing.T) {
	windows := &fakeWindows{
		windows:  map[float64][]subtitlePacket{1000: pgsCues(1, 4, 900, 1000)},
		failures: map[float64]error{2000: errors.New("ffprobe: exit status 1")},
	}
	record := filmWithImageSubtitles()

	err := measureImageSubtitles(t.Context(), windows.read, "/library/a.mkv", &record)

	if err == nil || record.Measured {
		t.Errorf("error = %v, measured %v, want an error and no measured flag", err, record.Measured)
	}
}

// A PGS cue stays on screen until the next composition, so the cue still
// shown when the window ends has no clear in the window. It counts.
func TestACueOnScreenWhenTheWindowEndsCounts(t *testing.T) {
	last := pgsCues(1, 10, 900, 1000)[18]
	windows := &fakeWindows{windows: map[float64][]subtitlePacket{
		1000: inOrder(pgsCues(1, 9, 900, 1000), []subtitlePacket{last}, vobsubCues(3, 10, 400, 1000)),
	}}

	record := measureWith(t, windows, filmWithImageSubtitles())

	if record.Streams[1].Image == nil {
		t.Errorf("image = none, want the numbers of the 9 cues and the one on screen")
	}
}

// A packet whose data is not a dump that ffprobe writes is dropped, and the
// packets around it count.
func TestAPacketThatDoesNotReadIsDropped(t *testing.T) {
	packets := vobsubCues(3, 10, 400, 1000)
	broken := []subtitlePacket{{Stream: 3, Time: "999.000", Data: "\n00000000: zz"}}
	windows := &fakeWindows{windows: map[float64][]subtitlePacket{1000: inOrder(broken, packets)}}

	record := measureWith(t, windows, filmWithImageSubtitles())

	if record.Streams[3].Image == nil {
		t.Errorf("image = none, want the numbers of the 10 cues that read")
	}
}

// The header answer of a film of 4000 seconds with one PGS stream.
const ffprobeOfAFilmWithAPGSStream = `{"format": {"duration": "4000.000", "bit_rate": "5000000"}, "streams": [
	{"codec_type": "video", "codec_name": "h264", "width": 1920, "height": 1080},
	{"codec_type": "subtitle", "codec_name": "hdmv_pgs_subtitle", "tags": {"language": "eng"}}]}`

// answeringWindows hands the probe fact the fake in place of ffprobe's
// window read, for the test.
func answeringWindows(t *testing.T, windows *fakeWindows) {
	t.Helper()
	was := readSubtitleWindow
	t.Cleanup(func() { readSubtitleWindow = was })
	readSubtitleWindow = windows.read
}

// The probe's record of the one film of a probe test, and the result of the
// probe's attempt at it.
func probedFilm(t *testing.T, windows *fakeWindows) (probedFile, string) {
	t.Helper()
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Long Survey (1982)", "The Long Survey (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	answeringWindows(t, windows)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfAFilmWithAPGSStream)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, "The Long Survey (1982)"), factProbe)
	if err != nil {
		t.Fatal(err)
	}
	record, _ := ledger.probeAt("The Long Survey (1982).mkv")
	if len(ledger.Attempts) != 1 {
		t.Fatalf("attempts = %+v, want one", ledger.Attempts)
	}
	return record, ledger.Attempts[0].Result
}

func TestTheProbeRecordsEachImageStreamsMeasurementInTheLedger(t *testing.T) {
	record, result := probedFilm(t, &fakeWindows{windows: map[float64][]subtitlePacket{1000: pgsCues(1, 20, 900, 1000)}})

	want := []*probedImage{nil, {CanvasHeight: 1080, LineHeight: 60, InkBottom: 960}}
	if got := imagesOf(record); !reflect.DeepEqual(got, want) || !record.Measured || result != attemptFound {
		t.Errorf("images = %+v, measured %v, attempt %s, want %+v, measured, and found", got, record.Measured, result, want)
	}
}

// A window read that fails still records what the header read found, so
// the screens have the file's codecs and tracks. The record is unmeasured
// and the attempt is an error, so the probe reads the file again after the
// error's window.
func TestAFailedWindowReadKeepsTheHeaderFacts(t *testing.T) {
	record, result := probedFilm(t, &fakeWindows{err: errors.New("ffprobe: exit status 1")})

	if len(record.Streams) != 2 || record.Duration != 4000 || record.Measured || result != attemptError {
		t.Errorf("record = %+v, attempt %s, want the header's two streams, unmeasured, and an error", record, result)
	}
}
