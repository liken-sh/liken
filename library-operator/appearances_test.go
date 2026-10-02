package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The appearances fact's gap, and what its worker does with one video: the
// two runs of the tool, the ledger it writes, and what each failure records.

// The library and the titles every case below works on. The titles are
// invented.
const (
	appearancesLibrary = "house/movies"
	appearancesFolder  = "Paper Lanterns (1961)"
	appearancesFile    = "Paper Lanterns (1961).mkv"
	appearancesSeries  = "Low Tide"
	appearancesSeason  = "Low Tide/Season 01"
	appearancesEpisode = "Low Tide - S01E01.mkv"
)

// The two model files the stand-in image holds, and the hash of each, which
// the header of a current detections record names.
var (
	appearancesDetector = detectionsModel{Name: "face_detection_yunet_2023mar", SHA256: sha256Hex("detector")}
	appearancesEmbedder = detectionsModel{Name: "face_recognition_sface_2021dec", SHA256: sha256Hex("embedder")}
)

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// A model directory with the two files, named for the worker the way the
// image's environment names it.
func seedAppearancesModels(t *testing.T) {
	t.Helper()
	models := t.TempDir()
	writeFile(t, filepath.Join(models, appearancesDetector.Name+".onnx"), "detector")
	writeFile(t, filepath.Join(models, appearancesEmbedder.Name+".onnx"), "embedder")
	t.Setenv(appearancesModelsVariable, models)
}

// The header line of a detections record of a file of size bytes, made by
// the two models above.
func detectionsLine(t *testing.T, size int64) string {
	t.Helper()
	header, err := json.Marshal(detectionsHeader{Format: detectionsFormat, Size: size,
		Detector: appearancesDetector, Embedder: appearancesEmbedder})
	if err != nil {
		t.Fatal(err)
	}
	return string(header) + "\n"
}

// The header line of a record of an earlier format, which held the
// keyframes alone, of a file of size bytes.
func earlierFormatDetectionsLine(t *testing.T, size int64) string {
	t.Helper()
	return strings.Replace(detectionsLine(t, size), detectionsFormat, "liken.sh/appearances/detections/v1", 1)
}

// One match summary, as the tool prints it, for one video file of size
// bytes: two people in the gallery, one with no headshot, and three faces
// named for one person.
func matchDocumentFor(file string, size int64) string {
	return `{"format":"liken.sh/appearances/summary/v1",` +
		`"embedder":{"name":"face_recognition_sface_2021dec","sha256":"` + appearancesEmbedder.SHA256 + `"},` +
		`"threshold":0.363,"margin":0.05,` +
		`"gallery":[{"contributor":".contributors/ad/ada-quill","name":"Ada Quill","headshot":"found","sha256":"ab12"},` +
		`{"contributor":".contributors/bo/bo-reyes","name":"Bo Reyes","headshot":"missing"}],` +
		`"unmatched":[{"contributor":".contributors/bo/bo-reyes","name":"Bo Reyes","headshot":"missing"}],` +
		`"files":{"` + file + `":{"size":` + itoa(size) + `,"named":{"headshot":2,"film":1},"people":1}}}`
}

func itoa(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

// What the stand-in tool does on one run.
type standInAppearances struct {
	// The detections record detect writes beside the video, and empty for a
	// detect that fails.
	record string
	// A detect given --hwaccel fails, the way a codec the render node
	// refuses fails.
	refuseHardware bool
	// The document match prints, and empty for a match that fails.
	document string
}

// A stand-in for the appearances binary. It appends each run's arguments to a
// file, one line per run, and returns the path of that file. detect copies
// the record into .liken/appearances/ beside the video, as the tool names it,
// and match prints the document.
func (s standInAppearances) install(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	record := filepath.Join(dir, "record.jsonl")
	document := filepath.Join(dir, "document.json")
	writeFile(t, record, s.record)
	writeFile(t, document, s.document)
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\nfor last; do :; done\ncase \"$1\" in\n" +
		"detect)\n"
	if s.refuseHardware {
		script += "  case \"$*\" in *--hwaccel*) echo 'appearances: ffmpeg exited with exit status: 1' >&2; exit 1;; esac\n"
	}
	if s.record == "" {
		script += "  echo 'appearances: ffmpeg exited with exit status: 1' >&2\n  exit 1\n"
	} else {
		script += "  out=\"$(dirname \"$last\")/.liken/appearances\"\n  mkdir -p \"$out\"\n" +
			"  cp '" + record + "' \"$out/$(basename \"$last\").jsonl\"\n" +
			"  echo \"$(basename \"$last\"): 1 samples, 1 of them keyframes, 1 faces on CPU\" >&2\n"
	}
	script += "  ;;\nmatch)\n"
	if s.document == "" {
		script += "  echo 'appearances: no .contributors/ folder at or above the title' >&2\n  exit 1\n"
	} else {
		script += "  cat '" + document + "'\n"
	}
	script += "  ;;\nesac\n"
	binary := filepath.Join(dir, "appearances")
	writeFile(t, binary, script)
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatal(err)
	}
	held := appearancesBinary
	t.Cleanup(func() { appearancesBinary = held })
	appearancesBinary = binary
	return calls
}

// The runs the stand-in saw, one argument list per run.
func appearancesCalls(t *testing.T, calls string) []string {
	t.Helper()
	data, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// One movie's video on the volume, and the line a work list holds for it.
func seedAppearancesMovie(t *testing.T, root string) workItem {
	t.Helper()
	writeFile(t, filepath.Join(root, appearancesFolder, appearancesFile), "video")
	return workItem{Path: filepath.Join(appearancesFolder, appearancesFile), Size: int64(len("video")),
		DurationMs: 6000000, Listed: time.Now().UTC()}
}

// The appearances ledger of one folder.
func appearancesLedgerOf(t *testing.T, folder string) likenLedger {
	t.Helper()
	ledger, err := readLikenLedger(folder, factAppearances)
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

// A feature with a length, whose title credits an actor with a headshot, is
// the gap: a movie of its own, and an episode through its series. A title
// whose cast has no headshot, a title with no cast, a trailer, and a video
// the probe has not reached are none of it, because no face in them can be
// named.
func TestTheAppearancesGapAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	video := func(path string, items ...string) fileRow {
		return fileRow{Path: path, Library: appearancesLibrary, Present: true, Type: fileTypeVideo,
			Role: fileRolePrimary, DurationMs: 6000000, VideoCodec: "h264", SizeBytes: 4096, Items: items}
	}
	trailer := video("A/trailers/Official Trailer.mp4", "movie:tmdb:1")
	trailer.Role = fileRoleTrailer
	unprobed := video("D/d.mkv", "movie:tmdb:4")
	unprobed.DurationMs, unprobed.VideoCodec = 0, ""
	episode := episodeID("series:tvdb:9", 1, 1)
	seed := &walkResult{
		files: []fileRow{video("A/a.mkv", "movie:tmdb:1"), video("B/b.mkv", "movie:tmdb:2"),
			video("C/c.mkv", "movie:tmdb:3"), unprobed, trailer,
			video("S/Season 01/s01e01.mkv", episode)},
		episodes: []episodeRow{{Id: episode, Library: appearancesLibrary, Kind: libraryKindSeries,
			Path: "S/Season 01/s01e01.mkv", Series: "series:tvdb:9", Season: 1, Episode: 1}},
		credits: []creditRow{
			{Library: appearancesLibrary, Item: "movie:tmdb:1", Contributor: ".contributors/ad/ada-quill", Part: creditPartActor},
			{Library: appearancesLibrary, Item: "movie:tmdb:2", Contributor: ".contributors/bo/bo-reyes", Part: creditPartActor},
			{Library: appearancesLibrary, Item: "movie:tmdb:2", Contributor: ".contributors/ad/ada-quill", Part: creditPartDirector, Billing: 1},
			{Library: appearancesLibrary, Item: "movie:tmdb:4", Contributor: ".contributors/ad/ada-quill", Part: creditPartActor},
			{Library: appearancesLibrary, Item: "series:tvdb:9", Contributor: ".contributors/ad/ada-quill", Part: creditPartActor},
		},
		contributors: []contributorRow{
			{Library: appearancesLibrary, Path: ".contributors/ad/ada-quill", Name: "Ada Quill", Headshot: true},
			{Library: appearancesLibrary, Path: ".contributors/bo/bo-reyes", Name: "Bo Reyes"},
		},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}

	items, err := catalog.workItems(t.Context(), factAppearances, appearancesLibrary, ledgerTime, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	var paths []string
	for _, item := range items {
		paths = append(paths, item.Path)
	}
	if want := []string{"A/a.mkv", "S/Season 01/s01e01.mkv"}; !slices.Equal(paths, want) {
		t.Errorf("work list = %v, want %v", paths, want)
	}
	if len(items) > 0 && (items[0].Size != 4096 || items[0].DurationMs != 6000000) {
		t.Errorf("item = %+v, want the size and the length the worker checks", items[0])
	}
}

// A found attempt closes the file for good, because the faces of one file do
// not change. A miss is asked again after the dated window, and an error after
// a day.
func TestAnAppearancesAttemptHoldsItsFileOutOfTheGap(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name   string
		result string
		ago    time.Duration
		gap    bool
	}{
		{name: "found yesterday", result: attemptFound, ago: day},
		{name: "found two months ago", result: attemptFound, ago: 60 * day},
		{name: "nothing yesterday", result: attemptNothing, ago: day},
		{name: "nothing two months ago", result: attemptNothing, ago: 60 * day, gap: true},
		{name: "an error an hour ago", result: attemptError, ago: time.Hour},
		{name: "an error two days ago", result: attemptError, ago: 2 * day, gap: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seed := &walkResult{
				files: []fileRow{{Path: "A/a.mkv", Library: appearancesLibrary, Present: true, Type: fileTypeVideo,
					Role: fileRolePrimary, DurationMs: 6000000, VideoCodec: "h264", Items: []string{"movie:tmdb:1"}}},
				credits: []creditRow{{Library: appearancesLibrary, Item: "movie:tmdb:1",
					Contributor: ".contributors/ad/ada-quill", Part: creditPartActor}},
				contributors: []contributorRow{{Library: appearancesLibrary, Path: ".contributors/ad/ada-quill",
					Headshot: true}},
				attempts: []attemptRow{{Library: appearancesLibrary, Item: "A/a.mkv", Fact: factAppearances,
					At: ledgerTime.Add(-test.ago).Unix(), Result: test.result}},
			}
			if err := upsertWalk(t.Context(), catalog, seed); err != nil {
				t.Fatal(err)
			}

			gaps, err := catalog.gapCounts(t.Context(), appearancesLibrary, ledgerTime, nil)

			if err != nil {
				t.Fatal(err)
			}
			if (gaps[factAppearances] == 1) != test.gap {
				t.Errorf("appearances gap = %d, want a gap: %v", gaps[factAppearances], test.gap)
			}
		})
	}
}

// A new encode under the old name is another file, so the walk drops the
// appearances attempt of the earlier one and the gap opens.
func TestAReplacedFileReopensTheAppearances(t *testing.T) {
	cases := []struct {
		name     string
		replaced bool
		lifted   bool
	}{
		{name: "the same file", lifted: true},
		{name: "a new encode", replaced: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			episode := seedEnrichedEpisode(t)
			writeFactLedger(t, episode.season, factAppearances, likenLedger{
				Attempts: []likenAttempt{{Path: replacedEntry, At: episode.made, Result: attemptFound}}})
			if test.replaced {
				episode.replace(t)
			}

			result := readFolder(episode.scan(), episode.series)

			if got := slices.Contains(liftedFacts(result, episode.path), factAppearances); got != test.lifted {
				t.Errorf("appearances lifted = %v, want %v", got, test.lifted)
			}
		})
	}
}

// One movie from start to end: detect on the CPU, then the match of the title
// folder with the title's own credits, then the ledger with every input the
// answer depends on.
func TestTheAppearancesWorkerWritesTheLedger(t *testing.T) {
	root := t.TempDir()
	item := seedAppearancesMovie(t, root)
	seedAppearancesModels(t)
	seedRenderNodes(t)
	calls := standInAppearances{record: detectionsLine(t, item.Size),
		document: matchDocumentFor(appearancesFile, item.Size)}.install(t)
	work, log := testFactWorker(t, appearancesWorker, libraryKindMovies, root)

	work.appearancesOne(t.Context(), item)

	folder := filepath.Join(root, appearancesFolder)
	runs := appearancesCalls(t, calls)
	wantRuns := []string{
		"detect --device auto --decode-threads 2 " + filepath.Join(folder, appearancesFile),
		"match --device cpu --credits " + filepath.Join(folder, likenDirectory, "credits.yaml") + " " + folder,
	}
	if !slices.Equal(runs, wantRuns) {
		t.Errorf("runs = %q, want %q", runs, wantRuns)
	}
	ledger := appearancesLedgerOf(t, folder)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound || ledger.Attempts[0].Path != appearancesFile {
		t.Errorf("attempts = %+v, want one found attempt at the file", ledger.Attempts)
	}
	want := appearancesEntry{
		Path: appearancesFile, Size: item.Size,
		Embedder:  detectionsModel{Name: appearancesEmbedder.Name, SHA256: appearancesEmbedder.SHA256},
		Threshold: 0.363, Margin: 0.05,
		Gallery: []appearancesPerson{
			{Contributor: ".contributors/ad/ada-quill", Name: "Ada Quill", Headshot: "found", SHA256: "ab12"},
			{Contributor: ".contributors/bo/bo-reyes", Name: "Bo Reyes", Headshot: "missing"},
		},
		Unmatched: []appearancesPerson{{Contributor: ".contributors/bo/bo-reyes", Name: "Bo Reyes", Headshot: "missing"}},
		Named:     appearancesNamed{Headshot: 2, Film: 1}, People: 1,
	}
	if len(ledger.Appearances) != 1 || !sameAppearances(ledger.Appearances[0], want) {
		t.Errorf("appearances = %+v, want %+v", ledger.Appearances, want)
	}
	wantOneLine(t, log, "named 3 faces of 1 person in "+work.named(filepath.Join(folder, appearancesFile)),
		"1 of the 2 credited actors cannot be matched")
	if strings.Contains(log.String(), "Paper Lanterns") || strings.Contains(log.String(), "Ada Quill") {
		t.Errorf("log = %q, want no title and no name", log)
	}
}

func sameAppearances(a, b appearancesEntry) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

// With a render node in the pod, detect decodes on it and keeps the GPU's
// compiled kernels in the scratch directory. A decode the node refuses runs
// again in software, so one codec the GPU lacks costs one failed start and
// not the file.
func TestTheAppearancesWorkerDecodesOnTheRenderNode(t *testing.T) {
	cases := []struct {
		name   string
		refuse bool
		runs   []string
	}{
		{name: "the node decodes", runs: []string{
			"detect --device auto --decode-threads 2 --hwaccel vaapi --cache " + appearancesScratch}},
		{name: "the node refuses", refuse: true, runs: []string{
			"detect --device auto --decode-threads 2 --hwaccel vaapi --cache " + appearancesScratch,
			"detect --device auto --decode-threads 2 --cache " + appearancesScratch}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			item := seedAppearancesMovie(t, root)
			seedAppearancesModels(t)
			seedRenderNodes(t, "renderD128")
			calls := standInAppearances{record: detectionsLine(t, item.Size), refuseHardware: test.refuse,
				document: matchDocumentFor(appearancesFile, item.Size)}.install(t)
			work, _ := testFactWorker(t, appearancesWorker, libraryKindMovies, root)

			work.appearancesOne(t.Context(), item)

			var detects []string
			for _, run := range appearancesCalls(t, calls) {
				if strings.HasPrefix(run, "detect ") {
					detects = append(detects, strings.TrimSuffix(run, " "+filepath.Join(root, item.Path)))
				}
			}
			if !slices.Equal(detects, test.runs) {
				t.Errorf("detect runs = %q, want %q", detects, test.runs)
			}
			ledger := appearancesLedgerOf(t, filepath.Join(root, appearancesFolder))
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
				t.Errorf("attempts = %+v, want one found attempt", ledger.Attempts)
			}
		})
	}
}

// A detections record already on the volume is reused where its header names
// the file's size and the models the image holds, so a match that failed costs
// no second decode. A record of another size or another model is stale, and
// so is a record of an earlier format, which holds the keyframes alone.
func TestAnAppearancesWorkerReusesACurrentRecord(t *testing.T) {
	cases := []struct {
		name   string
		header func(t *testing.T, size int64) string
		detect bool
	}{
		{name: "a current record", header: detectionsLine},
		{name: "a record of another size", detect: true,
			header: func(t *testing.T, size int64) string { return detectionsLine(t, size+1) }},
		{name: "a record of another embedder", detect: true,
			header: func(t *testing.T, size int64) string {
				return strings.Replace(detectionsLine(t, size), appearancesEmbedder.SHA256, sha256Hex("older"), 1)
			}},
		{name: "a record of an earlier format", detect: true,
			header: func(t *testing.T, size int64) string { return earlierFormatDetectionsLine(t, size) }},
		{name: "a record that is not JSON", detect: true,
			header: func(*testing.T, int64) string { return "not json\n" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			item := seedAppearancesMovie(t, root)
			seedAppearancesModels(t)
			seedRenderNodes(t)
			writeFile(t, filepath.Join(root, appearancesFolder, likenDirectory, "appearances", appearancesFile+".jsonl"),
				test.header(t, item.Size))
			calls := standInAppearances{record: detectionsLine(t, item.Size),
				document: matchDocumentFor(appearancesFile, item.Size)}.install(t)
			work, _ := testFactWorker(t, appearancesWorker, libraryKindMovies, root)

			work.appearancesOne(t.Context(), item)

			detected := slices.ContainsFunc(appearancesCalls(t, calls), func(run string) bool {
				return strings.HasPrefix(run, "detect ")
			})
			if detected != test.detect {
				t.Errorf("detected = %v, want %v", detected, test.detect)
			}
		})
	}
}

// The credits fact credits a series and not its episodes, so an episode's
// gallery is the series' cast: the match of the season folder reads the
// series' credits file, and the ledger beside the episode is keyed by its
// file name.
func TestAnEpisodeMatchesTheSeriesCast(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, appearancesSeason, appearancesEpisode), "episode")
	item := workItem{Path: filepath.Join(appearancesSeason, appearancesEpisode), Size: int64(len("episode")),
		DurationMs: 2700000, Listed: time.Now().UTC()}
	seedAppearancesModels(t)
	seedRenderNodes(t)
	calls := standInAppearances{record: detectionsLine(t, item.Size),
		document: matchDocumentFor(appearancesEpisode, item.Size)}.install(t)
	work, _ := testFactWorker(t, appearancesWorker, libraryKindSeries, root)

	work.appearancesOne(t.Context(), item)

	season := filepath.Join(root, appearancesSeason)
	credits := filepath.Join(root, appearancesSeries, likenDirectory, "credits.yaml")
	if runs := appearancesCalls(t, calls); len(runs) != 2 || runs[1] != "match --device cpu --credits "+credits+" "+season {
		t.Errorf("runs = %q, want the match of the season folder with the series' credits", runs)
	}
	ledger := appearancesLedgerOf(t, season)
	if len(ledger.Appearances) != 1 || ledger.Appearances[0].Path != appearancesEpisode {
		t.Errorf("appearances = %+v, want the episode's entry beside it", ledger.Appearances)
	}
}

// Every way the work on one video fails is an error attempt with the tool's
// own words, and the ledger holds no answer from it.
func TestWhatAFailedAppearancesRunRecords(t *testing.T) {
	cases := []struct {
		name     string
		document func(size int64) string
		record   bool
		want     string
	}{
		{name: "detect fails", document: func(size int64) string { return matchDocumentFor(appearancesFile, size) },
			want: "ffmpeg exited with exit status: 1"},
		{name: "match fails", record: true, document: func(int64) string { return "" },
			want: "no .contributors/ folder at or above the title"},
		{name: "match names another file", record: true,
			document: func(size int64) string { return matchDocumentFor("another.mkv", size) },
			want:     "named no detections record"},
		{name: "match names another size", record: true,
			document: func(size int64) string { return matchDocumentFor(appearancesFile, size+1) },
			want:     "of another size"},
		{name: "match prints another format", record: true,
			document: func(int64) string { return `{"format":"liken.sh/appearances/summary/v9"}` },
			want:     "liken.sh/appearances/summary/v9"},
		{name: "match prints no JSON", record: true, document: func(int64) string { return "faces" },
			want: "reading the match document"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			item := seedAppearancesMovie(t, root)
			seedAppearancesModels(t)
			seedRenderNodes(t)
			tool := standInAppearances{document: test.document(item.Size)}
			if test.record {
				tool.record = detectionsLine(t, item.Size)
			}
			tool.install(t)
			work, log := testFactWorker(t, appearancesWorker, libraryKindMovies, root)

			work.appearancesOne(t.Context(), item)

			ledger := appearancesLedgerOf(t, filepath.Join(root, appearancesFolder))
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError ||
				!strings.Contains(ledger.Attempts[0].Reason, test.want) {
				t.Errorf("attempts = %+v, want one error that says %q", ledger.Attempts, test.want)
			}
			if len(ledger.Appearances) != 0 {
				t.Errorf("appearances = %+v, want none", ledger.Appearances)
			}
			if !strings.Contains(log.String(), test.want) {
				t.Errorf("log = %q, want the tool's words %q", log, test.want)
			}
		})
	}
}

// A worker Job that is deleted or replaced stops its run in the middle of a
// video. The video was not answered, so the run records no attempt and the
// video stays in the gap for the next worker. The killed tool leaves its
// partial files, and the run removes the ones its own pod wrote, by the
// host in their names. A partial file of another host may belong to a run
// that is still writing, so it stays.
func TestAStoppedAppearancesRunRecordsNothing(t *testing.T) {
	root := t.TempDir()
	item := seedAppearancesMovie(t, root)
	seedAppearancesModels(t)
	seedRenderNodes(t)
	t.Setenv("HOSTNAME", "movies-appearances-a1")
	records := filepath.Join(root, appearancesFolder, likenDirectory, "appearances")
	ours := filepath.Join(records, appearancesFile+".jsonl.partial-movies-appearances-a1-41")
	theirs := filepath.Join(records, appearancesFile+".jsonl.partial-movies-appearances-b2-41")
	writeFile(t, ours, "{}")
	writeFile(t, theirs, "{}")
	standInAppearances{record: detectionsLine(t, item.Size),
		document: matchDocumentFor(appearancesFile, item.Size)}.install(t)
	work, _ := testFactWorker(t, appearancesWorker, libraryKindMovies, root)
	ctx, stop := context.WithCancel(t.Context())
	stop()

	work.appearancesOne(ctx, item)

	ledger := appearancesLedgerOf(t, filepath.Join(root, appearancesFolder))
	if len(ledger.Attempts) != 0 {
		t.Errorf("attempts = %+v, want none", ledger.Attempts)
	}
	if left := namesIn(t, records); !slices.Equal(left, []string{filepath.Base(theirs)}) {
		t.Errorf("the records directory holds %v, want the other host's partial alone", left)
	}
}

// The worker's Job runs the appearances image with the memory the measured
// runs need, an emptyDir for the GPU's kernel cache, and the render claim
// where the Library names a render block.
func TestAnAppearancesWorkerJob(t *testing.T) {
	cases := []struct {
		name   string
		render *RenderDevice
	}{
		{name: "on the CPU"},
		{name: "on a GPU", render: &RenderDevice{Class: "gpu.liken.sh"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Appearances = LibraryAppearances{Enabled: true, Render: test.render}

			pod := buildFactWorkerJob(library, appearancesWorker,
				jobImages{operator: testScannerImage, ffmpeg: testFFmpegImage, appearances: testAppearancesImage},
				"", "movies-walk-1", testNow).Spec.Template.Spec

			container := pod.Containers[0]
			if container.Name != factAppearances || container.Image != testAppearancesImage ||
				container.Command[1] != workerMode {
				t.Errorf("container = %s on %s running %v, want the appearances worker", container.Name,
					container.Image, container.Command)
			}
			if container.Resources.Limits["memory"] != "1536Mi" {
				t.Errorf("memory limit = %q, want 1536Mi", container.Resources.Limits["memory"])
			}
			if len(pod.Volumes) != 2 || pod.Volumes[1].EmptyDir == nil ||
				!slices.ContainsFunc(container.VolumeMounts, func(m VolumeMount) bool {
					return m.Name == pod.Volumes[1].Name && m.MountPath == appearancesScratch
				}) {
				t.Errorf("volumes = %+v mounted at %+v, want the scratch emptyDir at %s",
					pod.Volumes, container.VolumeMounts, appearancesScratch)
			}
			claimed := len(pod.ResourceClaims) == 1 && pod.ResourceClaims[0].ResourceClaimTemplateName == "movies-appearances"
			if claimed != (test.render != nil) {
				t.Errorf("claims = %+v, want the appearances template: %v", pod.ResourceClaims, test.render != nil)
			}
		})
	}
}

// A video is quick for the appearances worker where a detections record of
// its size and of the current format is on the volume, because the worker
// then runs only the match.
func TestAVideoWithARecordOfItsSizeIsQuick(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Film", "Film.mkv")
	writeFile(t, video, "film")
	run := &factWorkerRun{root: root}
	item := workItem{Path: "Film/Film.mkv", Size: 4}

	if appearancesQuick(run, item) {
		t.Error("a video with no record is quick")
	}
	writeFile(t, detectionsPath(video), detectionsLine(t, 5))
	if appearancesQuick(run, item) {
		t.Error("a video with a record of another size is quick")
	}
	writeFile(t, detectionsPath(video), earlierFormatDetectionsLine(t, 4))
	if appearancesQuick(run, item) {
		t.Error("a video with a record of an earlier format is quick")
	}
	writeFile(t, detectionsPath(video), detectionsLine(t, 4))
	if !appearancesQuick(run, item) {
		t.Error("a video with a record of its size is not quick")
	}
}
