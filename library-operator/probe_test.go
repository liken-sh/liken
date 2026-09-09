package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The answers these tests hand the container, as ffprobe itself wrote them
// about four real files, with the names and paths removed.
var (
	ffprobeOfOneFile          = ffprobeCapture("video-1080p-h264.json")
	ffprobeOfAnHDRFile        = ffprobeCapture("video-2160p-hdr10.json")
	ffprobeOfADolbyVisionFile = ffprobeCapture("video-2160p-dolbyvision.json")
	ffprobeOfAMusicFile       = ffprobeCapture("audio-mp3-with-cover.json")
)

// A capture that cannot be read stops the package, because every probe test
// works from one.
func ffprobeCapture(name string) string {
	data, err := os.ReadFile(filepath.Join("testdata", "ffprobe", name))
	if err != nil {
		panic(err)
	}
	return string(data)
}

// The probe a test hands the container. It answers for every file and never
// opens one.
func answeringProbe(answer string) mediaProbe {
	return func(context.Context, string) ([]byte, error) {
		return []byte(answer), nil
	}
}

// seedProbeGap seeds one movie title with one video file no probe has
// read, which is the shape of a probe gap.
func seedProbeGap(t *testing.T, catalog *Catalog, root, folder, file string) {
	t.Helper()
	writeFile(t, filepath.Join(root, folder, file), "video")
	path := filepath.Join(folder, file)
	seed := &walkResult{
		movies: []movieRow{{Id: "movie:path:x", Library: "house/movies", Kind: libraryKindMovies, Path: folder, Title: folder}},
		files: []fileRow{{
			Path: path, Library: "house/movies", Present: true, Type: fileTypeVideo,
			Modified: modifiedOf(t, filepath.Join(root, path)), Items: []string{"movie:path:x"},
		}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

func TestTheProbeWritesStreamDetailsIntoAMinimalSidecar(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	sidecar := readFileString(t, filepath.Join(root, "The Thing (1982)", movieSidecarName))
	for _, want := range []string{"<title>The Thing</title>", "<codec>h264</codec>", "<width>1920</width>",
		"<durationinseconds>8673</durationinseconds>", "<aspect>1.78</aspect>",
		"<codec>ac3</codec>", "<channels>6</channels>", "<subtitle>"} {
		if !strings.Contains(sidecar, want) {
			t.Errorf("the sidecar holds no %s:\n%s", want, sidecar)
		}
	}
	if !strings.Contains(log.String(), "probed 1 of the 1 files") {
		t.Errorf("log = %q, want the count of files probed", log.String())
	}
}

func TestTheProbeSidecarIsReadBackAsTheStreamTheScannerWants(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, "The Thing (1982)", movieSidecarName))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := parseMovieNFO(data)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Stream.Width != 1920 || meta.Stream.VideoCodec != "h264" || meta.Stream.AudioCodec != "ac3" {
		t.Errorf("stream = %+v, want the resolution and the codecs", meta.Stream)
	}
	if meta.Duration != 8673 {
		t.Errorf("duration = %d, want the container's own seconds", meta.Duration)
	}
}

func TestTheProbeRecordsItsAttemptAndTheRunItStarted(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, "The Thing (1982)"), factProbe)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("ledger = %+v, want one attempt that found the details", ledger.Attempts)
	}
	if ledger.Attempts[0].Path != "The Thing (1982).mkv" {
		t.Errorf("the attempt names %q, want the file", ledger.Attempts[0].Path)
	}
	if got := agent.rowCount(t, "runs"); got != 1 {
		t.Errorf("runs = %d, want the started mark the probe container writes", got)
	}
}

func TestTheProbeKeepsEveryOtherByteOfASidecarThatIsAlreadyThere(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	sidecar := filepath.Join(root, "The Thing (1982)", movieSidecarName)
	writeFile(t, sidecar, nfoWithUnknownElements)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	edited := readFileString(t, sidecar)
	for _, want := range []string{"<lockdata>false</lockdata>", "<criticrating>84</criticrating>",
		`<uniqueid type="imdb">tt0084787</uniqueid>`, "<poster>/volume/poster.jpg</poster>"} {
		if !strings.Contains(edited, want) {
			t.Errorf("the edit lost %s:\n%s", want, edited)
		}
	}
	if !strings.Contains(edited, "<streamdetails>") {
		t.Errorf("the edit wrote no stream details:\n%s", edited)
	}
}

func TestTheProbeRecordsAnErrorWhereTheFileWillNotOpen(t *testing.T) {
	cases := []struct {
		name  string
		probe mediaProbe
	}{
		{
			name:  "ffprobe fails",
			probe: func(context.Context, string) ([]byte, error) { return nil, errors.New("no such file") },
		},
		{
			name:  "ffprobe answers something that is not JSON",
			probe: answeringProbe("not json"),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)

			if err := work.probeGap(t.Context(), test.probe); err != nil {
				t.Fatal(err)
			}

			ledger, err := readLikenLedger(filepath.Join(root, "The Thing (1982)"), factProbe)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
				t.Errorf("ledger = %+v, want one attempt that ended in an error", ledger.Attempts)
			}
		})
	}
}

func TestTheProbeWorksOverTheFolderItsJobNames(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	writeFile(t, filepath.Join(root, "Alien (1979)", "Alien (1979).mkv"), "video")
	if err := upsertWalk(t.Context(), catalog, &walkResult{files: []fileRow{
		{Path: filepath.Join("Alien (1979)", "Alien (1979).mkv"), Library: "house/movies", Present: true, Type: fileTypeVideo},
	}}); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.scanPath = "The Thing (1982)"
	work.scope = work.narrowedScope()

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(root, "The Thing (1982)", movieSidecarName)); err != nil {
		t.Errorf("the folder the Job named holds no sidecar: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Alien (1979)", movieSidecarName)); err == nil {
		t.Error("the probe wrote outside the folder its Job named")
	}
}

func TestTheProbeFailsWhereItCannotReadItsGap(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(),
		NewCatalog("http://127.0.0.1:1", &http.Client{Timeout: time.Second}))

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err == nil {
		t.Error("the probe reported no error, want the unreachable sidecar's")
	}
}

func TestTheSidecarAFileWritesIntoIsTheOneTheScannerReads(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Thing (1982)", "The Thing 1.mkv"), "video")
	writeFile(t, filepath.Join(root, "The Thing (1982)", "The Thing 2.mkv"), "video")
	writeFile(t, filepath.Join(root, "The Thing (1982)", "trailers", "teaser.mkv"), "video")
	writeFile(t, filepath.Join(root, "Twin Peaks", "Season 01", "s01e01.mkv"), "video")

	cases := []struct {
		name        string
		kind        string
		file        string
		wantSidecar string
		wantRoot    string
		wantTitle   string
	}{
		{
			name:        "the first video of a movie folder in name order",
			kind:        libraryKindMovies,
			file:        "The Thing (1982)/The Thing 1.mkv",
			wantSidecar: "The Thing (1982)/movie.nfo",
			wantRoot:    nfoRootMovie,
			wantTitle:   "The Thing",
		},
		{
			name:        "a second encoding beside it",
			kind:        libraryKindMovies,
			file:        "The Thing (1982)/The Thing 2.mkv",
			wantSidecar: "The Thing (1982)/The Thing 2.nfo",
			wantRoot:    nfoRootMovie,
			wantTitle:   "The Thing 2",
		},
		{
			name:        "a trailer in an extras folder",
			kind:        libraryKindMovies,
			file:        "The Thing (1982)/trailers/teaser.mkv",
			wantSidecar: "The Thing (1982)/trailers/teaser.nfo",
			wantRoot:    nfoRootMovie,
			wantTitle:   "teaser",
		},
		{
			name:        "an episode",
			kind:        libraryKindSeries,
			file:        "Twin Peaks/Season 01/s01e01.mkv",
			wantSidecar: "Twin Peaks/Season 01/s01e01.nfo",
			wantRoot:    nfoRootEpisode,
			wantTitle:   "s01e01",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			sidecar, rootElement, title := probeSidecar(test.kind, filepath.Join(root, test.file))
			if sidecar != filepath.Join(root, test.wantSidecar) {
				t.Errorf("sidecar = %q, want %q", sidecar, test.wantSidecar)
			}
			if rootElement != test.wantRoot || title != test.wantTitle {
				t.Errorf("root, title = %q, %q, want %q, %q", rootElement, title, test.wantRoot, test.wantTitle)
			}
		})
	}
}

func TestADurationReadsBackAsWholeSeconds(t *testing.T) {
	cases := []struct {
		name  string
		value float64
		want  int
	}{
		{name: "a decimal that rounds down", value: 6540.4, want: 6540},
		{name: "a decimal that rounds up", value: 6540.6, want: 6541},
		{name: "no duration at all", value: 0, want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := probeSeconds(test.value); got != test.want {
				t.Errorf("probeSeconds(%v) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

func TestTheStreamDetailsAreWrittenFromTheRecord(t *testing.T) {
	cases := []struct {
		name   string
		record probedFile
		want   nfoStreamDetailsBody
	}{
		{
			name: "a plain video with an audio track and a subtitle",
			record: probedFile{Duration: 6540.4, Streams: []probedStream{
				{Kind: fileTypeVideo, Codec: "h264", Width: 1920, Height: 1080},
				{Kind: fileTypeAudio, Codec: "ac3", Channels: 6, Language: "eng"},
				{Kind: fileTypeSubtitle, Codec: "subrip", Language: "eng"},
			}},
			want: nfoStreamDetailsBody{
				Video: []nfoVideoElement{{Codec: "h264", Aspect: "1.78", Width: 1920,
					Height: 1080, Duration: 6540}},
				Audio:    []nfoAudioElement{{Codec: "ac3", Channels: 6, Language: "eng"}},
				Subtitle: []nfoSubtitleElement{{Language: "eng"}},
			},
		},
		{
			name: "dolby vision over an HDR10 transfer",
			record: probedFile{Streams: []probedStream{{Kind: fileTypeVideo, Codec: "hevc",
				Width: 3840, Height: 2160, DolbyVision: true,
				Color: probedColor{Transfer: "smpte2084"}}}},
			want: nfoStreamDetailsBody{Video: []nfoVideoElement{{Codec: "hevc", Aspect: "1.78",
				Width: 3840, Height: 2160, HDRType: "dolbyvision"}}},
		},
		{
			name: "an HDR10 transfer",
			record: probedFile{Streams: []probedStream{{Kind: fileTypeVideo, Codec: "hevc",
				Width: 3840, Height: 1600, Color: probedColor{Transfer: "smpte2084"}}}},
			want: nfoStreamDetailsBody{Video: []nfoVideoElement{{Codec: "hevc", Aspect: "2.40",
				Width: 3840, Height: 1600, HDRType: "hdr10"}}},
		},
		{
			name: "an HLG transfer",
			record: probedFile{Streams: []probedStream{{Kind: fileTypeVideo, Codec: "hevc",
				Width: 3840, Height: 2160, Color: probedColor{Transfer: "arib-std-b67"}}}},
			want: nfoStreamDetailsBody{Video: []nfoVideoElement{{Codec: "hevc", Aspect: "1.78",
				Width: 3840, Height: 2160, HDRType: "hlg"}}},
		},
		{
			name: "a cover and a data stream, which the sidecar does not carry",
			record: probedFile{Streams: []probedStream{
				{Kind: fileTypeImage, Codec: "mjpeg", Width: 600, Height: 600},
				{Kind: probedKindData, Codec: "bin_data"},
			}},
			want: nfoStreamDetailsBody{},
		},
		{
			name: "a video of no stated size",
			record: probedFile{Streams: []probedStream{
				{Kind: fileTypeVideo, Codec: "h264"},
			}},
			want: nfoStreamDetailsBody{Video: []nfoVideoElement{{Codec: "h264"}}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := test.record.fileInfo().StreamDetails

			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("stream details = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestTheProbeWritesTheWholeAnswerIntoItsOwnLedger(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfAnHDRFile)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, "The Thing (1982)"), factProbe)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Probes) != 1 {
		t.Fatalf("probes = %+v, want one record", ledger.Probes)
	}
	record := ledger.Probes[0]
	if record.Path != "The Thing (1982).mkv" || record.Container != "mkv" {
		t.Errorf("record = %+v, want the file's own path and container", record)
	}
	if record.Duration != 8462.752 || record.Bitrate != 19535025 || len(record.Streams) != 4 {
		t.Errorf("record = %+v, want the container's facts and every stream", record)
	}
	if record.Size != int64(len("video")) || record.Modified == 0 || record.At.IsZero() {
		t.Errorf("record = %+v, want the file's size, its change time, and the time of the probe", record)
	}
}

func TestASecondProbeOfAFileLeavesOneRecordInTheLedger(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	for range 2 {
		if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
			t.Fatal(err)
		}
	}

	ledger, err := readLikenLedger(filepath.Join(root, "The Thing (1982)"), factProbe)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Probes) != 1 {
		t.Errorf("probes = %+v, want the one record the second run replaced", ledger.Probes)
	}
}

func TestAnAudioFileIsRecordedAndGetsNoSidecar(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A Perfect Circle", "01 The Track.mp3"), "audio")
	work, _ := testEnricher(t, libraryKindMovies, root, nil)

	work.probeOne(t.Context(), answeringProbe(ffprobeOfAMusicFile),
		filepath.Join("A Perfect Circle", "01 The Track.mp3"))

	ledger, err := readLikenLedger(filepath.Join(root, "A Perfect Circle"), factProbe)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Probes) != 1 || len(ledger.Probes[0].Streams) != 2 {
		t.Fatalf("probes = %+v, want the track and its cover", ledger.Probes)
	}
	if ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempt = %+v, want one that found the streams", ledger.Attempts[0])
	}
	if _, err := os.Stat(filepath.Join(root, "A Perfect Circle", "01 The Track.nfo")); err == nil {
		t.Error("the probe wrote a sidecar beside a music file")
	}
}

func TestAFileTheProbeCannotStatIsAnError(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)

	work.probeOne(t.Context(), answeringProbe(ffprobeOfOneFile),
		filepath.Join("The Thing (1982)", "The Thing (1982).mkv"))

	ledger, err := readLikenLedger(filepath.Join(root, "The Thing (1982)"), factProbe)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Probes) != 0 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("ledger = %+v, want no record and an error attempt", ledger)
	}
}

func TestASidecarThatIsNotXMLFailsTheEdit(t *testing.T) {
	dir := t.TempDir()
	sidecar := filepath.Join(dir, movieSidecarName)
	writeFile(t, sidecar, "this is not xml <<<")

	err := newVolumeWriter("movies-enrich").editNFO(sidecar, nfoRootMovie, "X",
		xmlElement{name: "fileinfo"}, []byte("<fileinfo/>"))

	if err == nil {
		t.Error("the edit reported no error, want one")
	}
}

func TestASidecarTheEnricherCannotReadFailsTheEdit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, movieSidecarName), 0o755); err != nil {
		t.Fatal(err)
	}

	err := newVolumeWriter("movies-enrich").editNFO(filepath.Join(dir, movieSidecarName), nfoRootMovie, "X",
		xmlElement{name: "fileinfo"}, []byte("<fileinfo/>"))

	if err == nil {
		t.Error("the edit reported no error, want one")
	}
}

// Runs the real command against a stand-in on the path, so the one call that
// starts a process is proved without a media file in the repository.
func TestTheRealCommandRunsAndAnswers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ffprobe"), "#!/bin/sh\necho '{\"format\":{\"duration\":\"1.0\"}}'\n")
	if err := os.Chmod(filepath.Join(dir, "ffprobe"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	output, err := ffprobeFile(t.Context(), filepath.Join(dir, "a.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "duration") {
		t.Errorf("output = %q, want the command's own answer", output)
	}
}

func TestACommandThatFailsIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ffprobe"), "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(filepath.Join(dir, "ffprobe"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if _, err := ffprobeFile(t.Context(), filepath.Join(dir, "a.mkv")); err == nil {
		t.Error("the command reported no error, want one")
	}
}

// A sidecar with no root element holds nothing to keep, so the minimal
// document takes its place and the fact's edit lands in it.
func TestASidecarWithNoRootElementIsWrittenAsIfItWereAbsent(t *testing.T) {
	cases := []struct {
		name    string
		sidecar string
	}{
		{name: "an empty file", sidecar: ""},
		{name: "an XML declaration alone", sidecar: "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s01e01.nfo")
			writeFile(t, path, test.sidecar)

			err := newVolumeWriter("series-enrich").editNFO(path, nfoRootEpisode, "s01e01",
				xmlElement{name: "fileinfo"}, []byte("<fileinfo/>"))

			if err != nil {
				t.Fatal(err)
			}
			want := "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<episodedetails>\n" +
				"  <title>s01e01</title>\n  <fileinfo/>\n</episodedetails>\n"
			if got := readFileString(t, path); got != want {
				t.Errorf("wrote %q, want %q", got, want)
			}
		})
	}
}

// A sidecar whose root the parser stops on is an error, and the bytes stay as
// they were, because the volume may hold facts no reader here models.
func TestASidecarWithARootTheParserCannotReadFailsTheEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s01e01.nfo")
	// The document ends inside the root, which even a lenient reader cannot
	// read past.
	sidecar := "<episodedetails><title>Breakage"
	writeFile(t, path, sidecar)

	err := newVolumeWriter("series-enrich").editNFO(path, nfoRootEpisode, "s01e01",
		xmlElement{name: "fileinfo"}, []byte("<fileinfo/>"))

	if err == nil {
		t.Error("the edit reported no error, want one")
	}
	if got := readFileString(t, path); got != sidecar {
		t.Errorf("the sidecar reads %q, want the bytes it held", got)
	}
}
