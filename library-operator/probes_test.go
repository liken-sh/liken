package main

// probes_test.go proves the walk's read of the probe ledger: the columns a
// fresh record fills, the stream rows it becomes, and what a stale record
// and an absent one leave alone.

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The ledger the probe fact leaves beside a title, written through the same
// types the fact writes it with.
func writeProbeLedger(t *testing.T, folder string, records ...probedFile) {
	t.Helper()
	data, err := yaml.Marshal(likenLedger{Probes: records})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(folder, likenDirectory, likenLedgerName(factProbe)), string(data))
}

// The modified stamp a record has to carry to be fresh.
func modifiedOf(t *testing.T, path string) int64 {
	t.Helper()
	_, modified, err := statFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return modified
}

// The columns a probe record fills, read off one file row.
type probedColumns struct {
	Container  string
	VideoCodec string
	AudioCodec string
	Width      int
	Height     int
	DurationMs int64
	Bitrate    int64
	Probed     int64
}

func probedColumnsOf(row fileRow) probedColumns {
	return probedColumns{
		Container: row.Container, VideoCodec: row.VideoCodec, AudioCodec: row.AudioCodec,
		Width: row.Width, Height: row.Height, DurationMs: row.DurationMs,
		Bitrate: row.Bitrate, Probed: row.Probed,
	}
}

// The stream rows of one file, in ordinal order.
func streamRowsAt(result *walkResult, path string) []streamRow {
	var rows []streamRow
	for _, row := range result.streams {
		if row.Path == path {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestAFreshProbeFillsAVideosColumns(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Thing (1982)")
	video := filepath.Join(folder, "The Thing (1982) 720p.mkv")
	writeFile(t, video, "video")
	writeProbeLedger(t, folder, probedFile{
		Path: "The Thing (1982) 720p.mkv", Modified: modifiedOf(t, video),
		Container: "mkv", Duration: 6540.4567, Bitrate: 8000000,
		Streams: []probedStream{
			{Kind: fileTypeVideo, Codec: "h264", Width: 1920, Height: 1080},
			{Kind: fileTypeAudio, Codec: "ac3", Channels: 6},
		},
	})

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	row := fileRowAt(t, result, "The Thing (1982)/The Thing (1982) 720p.mkv")
	want := probedColumns{
		Container: "mkv", VideoCodec: "h264", AudioCodec: "ac3", Width: 1920, Height: 1080,
		DurationMs: 6540457, Bitrate: 8000000, Probed: modifiedOf(t, video),
	}
	got := probedColumnsOf(row)
	if got != want {
		t.Errorf("row = %+v, want %+v", got, want)
	}
}

func TestAFreshProbeWritesOneStreamRowPerStream(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Thing (1982)")
	video := filepath.Join(folder, "The Thing (1982).mkv")
	writeFile(t, video, "video")
	writeProbeLedger(t, folder, probedFile{
		Path: "The Thing (1982).mkv", Modified: modifiedOf(t, video), Duration: 100,
		Streams: []probedStream{
			{
				Kind: fileTypeVideo, Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160,
				Depth: 10, FrameRate: "24000/1001",
				Color:       probedColor{Primaries: "bt2020", Transfer: "smpte2084", Space: "bt2020nc"},
				DolbyVision: true, Bitrate: 60000000, Language: "und", Title: "4K",
				Default: true, Forced: false,
			},
			{
				Kind: fileTypeAudio, Codec: "truehd", Profile: "Atmos", Channels: 8,
				Layout: "7.1", SampleRate: 48000, Bitrate: 4000000, Language: "eng",
				Title: "Surround", Default: true, Forced: false,
			},
			{Kind: fileTypeSubtitle, Codec: "subrip", Language: "eng", Forced: true},
		},
	})

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	path := "The Thing (1982)/The Thing (1982).mkv"
	want := []streamRow{
		{
			Library: "house/movies", Path: path, Ordinal: 0, Present: true,
			Kind: fileTypeVideo, Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160,
			Depth: 10, FrameRate: "24000/1001", ColorPrimaries: "bt2020",
			ColorTransfer: "smpte2084", ColorSpace: "bt2020nc", DolbyVision: true,
			Bitrate: 60000000, Language: "und", Title: "4K", Default: true,
		},
		{
			Library: "house/movies", Path: path, Ordinal: 1, Present: true,
			Kind: fileTypeAudio, Codec: "truehd", Profile: "Atmos", Channels: 8,
			Layout: "7.1", SampleRate: 48000, Bitrate: 4000000, Language: "eng",
			Title: "Surround", Default: true,
		},
		{
			Library: "house/movies", Path: path, Ordinal: 2, Present: true,
			Kind: fileTypeSubtitle, Codec: "subrip", Language: "eng", Forced: true,
		},
	}
	got := streamRowsAt(result, path)
	if len(got) != len(want) {
		t.Fatalf("streams = %+v, want %d rows", got, len(want))
	}
	for i, row := range got {
		if row != want[i] {
			t.Errorf("stream %d = %+v, want %+v", i, row, want[i])
		}
	}
}

func TestAStaleProbeLeavesTheColumnsToTheNameAndTheSidecar(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Thing (1982)")
	video := filepath.Join(folder, "The Thing (1982) 720p.mkv")
	writeFile(t, video, "video")
	writeProbeLedger(t, folder, probedFile{
		Path: "The Thing (1982) 720p.mkv", Modified: modifiedOf(t, video) - 1,
		Duration: 6540, Bitrate: 8000000,
		Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264", Width: 1920, Height: 1080}},
	})

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	path := "The Thing (1982)/The Thing (1982) 720p.mkv"
	row := fileRowAt(t, result, path)
	want := probedColumns{Container: "mkv", Width: 1280, Height: 720, Probed: modifiedOf(t, video) - 1}
	got := probedColumnsOf(row)
	if got != want {
		t.Errorf("row = %+v, want %+v", got, want)
	}
	if rows := streamRowsAt(result, path); rows != nil {
		t.Errorf("streams = %+v, want none from a record the file has outlived", rows)
	}
}

func TestAVideoWithNoProbeRecordKeepsTodaysColumns(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Thing (1982)")
	writeFile(t, filepath.Join(folder, "The Thing (1982) 720p.mkv"), "video")

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	path := "The Thing (1982)/The Thing (1982) 720p.mkv"
	row := fileRowAt(t, result, path)
	want := probedColumns{Container: "mkv", Width: 1280, Height: 720}
	got := probedColumnsOf(row)
	if got != want {
		t.Errorf("row = %+v, want %+v", got, want)
	}
	if rows := streamRowsAt(result, path); rows != nil {
		t.Errorf("streams = %+v, want none where no probe has read the file", rows)
	}
}

func TestAnAudioFileTakesItsColumnsFromTheRecordAndNoneFromItsName(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Thing (1982)")
	writeFile(t, filepath.Join(folder, "The Thing (1982).mkv"), "video")
	track := filepath.Join(folder, "The Thing (1982) 1080p-theme.mp3")
	writeFile(t, track, "audio")
	writeProbeLedger(t, folder, probedFile{
		Path: "The Thing (1982) 1080p-theme.mp3", Modified: modifiedOf(t, track),
		Duration: 212.5, Bitrate: 320000,
		Streams: []probedStream{{Kind: fileTypeAudio, Codec: "mp3", Channels: 2, SampleRate: 44100}},
	})

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	path := "The Thing (1982)/The Thing (1982) 1080p-theme.mp3"
	row := fileRowAt(t, result, path)
	want := probedColumns{
		Container: "mp3", AudioCodec: "mp3", DurationMs: 212500,
		Bitrate: 320000, Probed: modifiedOf(t, track),
	}
	got := probedColumnsOf(row)
	if got != want {
		t.Errorf("row = %+v, want %+v", got, want)
	}
	if rows := streamRowsAt(result, path); len(rows) != 1 || rows[0].Kind != fileTypeAudio {
		t.Errorf("streams = %+v, want the one audio stream", rows)
	}
}

func TestAnExtrasVideoReadsTheTitleFoldersProbeLedger(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Thing (1982)")
	writeFile(t, filepath.Join(folder, "The Thing (1982).mkv"), "video")
	extra := filepath.Join(folder, "Extras", "Deleted Scene.mkv")
	writeFile(t, extra, "video")
	writeProbeLedger(t, folder, probedFile{
		Path: filepath.Join("Extras", "Deleted Scene.mkv"), Modified: modifiedOf(t, extra),
		Duration: 61, Bitrate: 5000000,
		Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264", Width: 1280, Height: 720}},
	})

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	path := "The Thing (1982)/Extras/Deleted Scene.mkv"
	row := fileRowAt(t, result, path)
	if row.VideoCodec != "h264" || row.DurationMs != 61000 || row.Probed != modifiedOf(t, extra) {
		t.Errorf("row = %+v, want the record the title folder holds for the extra", row)
	}
	if rows := streamRowsAt(result, path); len(rows) != 1 {
		t.Errorf("streams = %+v, want the extra's one stream", rows)
	}
}

func TestAnEpisodeReadsTheSeasonFoldersProbeLedger(t *testing.T) {
	root := t.TempDir()
	season := filepath.Join(root, "Twin Peaks (1990)", "Season 01")
	episode := filepath.Join(season, "Twin Peaks - S01E01.mkv")
	writeFile(t, episode, "video")
	writeProbeLedger(t, season, probedFile{
		Path: "Twin Peaks - S01E01.mkv", Modified: modifiedOf(t, episode),
		Duration: 2760.25, Bitrate: 6000000,
		Streams: []probedStream{
			{Kind: fileTypeVideo, Codec: "mpeg2video", Width: 720, Height: 480},
			{Kind: fileTypeAudio, Codec: "ac3", Channels: 2},
		},
	})

	result := &walkResult{}
	scanSeriesFolder(folderScan{root: root, library: "house/series", kind: libraryKindSeries},
		filepath.Join(root, "Twin Peaks (1990)"), result)

	path := "Twin Peaks (1990)/Season 01/Twin Peaks - S01E01.mkv"
	row := fileRowAt(t, result, path)
	if row.VideoCodec != "mpeg2video" || row.AudioCodec != "ac3" || row.DurationMs != 2760250 {
		t.Errorf("row = %+v, want the record the season folder holds", row)
	}
	if row.Bitrate != 6000000 || row.Probed != modifiedOf(t, episode) {
		t.Errorf("row = %+v, want the record's rate and stamp", row)
	}
	if rows := streamRowsAt(result, path); len(rows) != 2 {
		t.Errorf("streams = %+v, want the episode's two streams", rows)
	}
}

// The whole path from a volume with a probe ledger to the rows a player
// reads, through the shipped schema.
func TestAProbedLibraryLandsItsFilesAndStreamsInTheCatalog(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := filepath.Join(root, "The Thing (1982)")
	video := filepath.Join(folder, "The Thing (1982).mkv")
	writeFile(t, video, "video")
	writeProbeLedger(t, folder, probedFile{
		Path: "The Thing (1982).mkv", Modified: modifiedOf(t, video),
		Container: "mkv", Duration: 6540.5, Bitrate: 8000000,
		Streams: []probedStream{
			{Kind: fileTypeVideo, Codec: "h264", Width: 1920, Height: 1080, Depth: 8},
			{Kind: fileTypeAudio, Codec: "ac3", Channels: 6, Language: "eng"},
		},
	})

	if err := upsertWalk(t.Context(), catalog, walkMovies(root, "house/movies", nil)); err != nil {
		t.Fatal(err)
	}

	files, err := catalog.queryStrings(t.Context(),
		`SELECT video_codec || ' ' || width || ' ' || duration_ms || ' ' || bitrate || ' ' || `+
			`CAST(probed = modified AS TEXT) FROM files WHERE library = ? AND type = 'video'`,
		[]any{"house/movies"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "h264 1920 6540500 8000000 1" {
		t.Errorf("file row = %v, want the probed columns", files)
	}
	streams, err := catalog.queryStrings(t.Context(),
		`SELECT ordinal || ' ' || kind || ' ' || codec || ' ' || channels || ' ' || language `+
			`FROM streams WHERE library = ? ORDER BY ordinal`, []any{"house/movies"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0 video h264 0 ", "1 audio ac3 6 eng"}
	if len(streams) != len(want) || streams[0] != want[0] || streams[1] != want[1] {
		t.Errorf("streams = %v, want %v", streams, want)
	}
	if got := agent.rowCount(t, "streams"); got != 2 {
		t.Errorf("stream rows = %d, want the file's two", got)
	}
}

// The files the probe container works from: every video and audio file
// whose probed column is not its modified column.
func TestTheProbeGapNamesEveryFileWithNoCurrentProbe(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	stamp := ledgerTime.Unix()
	files := []fileRow{
		{Path: "no probe.mkv", Type: fileTypeVideo, Modified: stamp},
		{Path: "probed.mkv", Type: fileTypeVideo, Modified: stamp, Probed: stamp},
		{Path: "changed.mkv", Type: fileTypeVideo, Modified: stamp, Probed: stamp - 1},
		{Path: "no probe.mp3", Type: fileTypeAudio, Modified: stamp},
		{Path: "probed.mp3", Type: fileTypeAudio, Modified: stamp, Probed: stamp},
		{Path: "no probe.srt", Type: fileTypeSubtitle, Modified: stamp},
		{Path: "gone.mkv", Type: fileTypeVideo, Modified: stamp},
	}
	seed := &walkResult{}
	for _, file := range files {
		file.Library = "house/movies"
		file.Present = file.Path != "gone.mkv"
		seed.files = append(seed.files, file)
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}

	gaps, err := catalog.queryStrings(t.Context(), gapQueries[factProbe],
		gapParams(factProbe, "house/movies", ledgerTime, time.Time{}))

	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(gaps)
	want := []string{"changed.mkv", "no probe.mkv", "no probe.mp3"}
	if !slices.Equal(gaps, want) {
		t.Errorf("gaps = %v, want %v", gaps, want)
	}
}
