package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stand-in for `titles train`. It appends each run's arguments to a file,
// and writes the model given to the file --out names, or fails with the
// tool's words where the model is empty.
func installStandInTrain(t *testing.T, model string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	source := filepath.Join(dir, "model.json")
	writeFile(t, source, model)
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\ncp \"$3\" '" + filepath.Join(dir, "labels.tsv") + "'\n"
	if model == "" {
		script += "echo 'titles: too few videos have a reading' >&2\nexit 1\n"
	} else {
		script += "cp '" + source + "' \"$5\"\n"
	}
	binary := filepath.Join(dir, "titles")
	writeFile(t, binary, script)
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatal(err)
	}
	held := titlesBinary
	t.Cleanup(func() { titlesBinary = held })
	titlesBinary = binary
	return dir
}

// A model file as `train` writes it, with the share of the held-out videos
// it placed within 30 seconds.
func trainedModel(within30 float64) string {
	data, _ := json.Marshal(map[string]any{
		"format": "liken.sh/titles/model/v1", "kind": titlesKindEnd,
		"trained":  map[string]any{"videos": 3, "groups": 2, "seconds": 900, "left_out": 1},
		"held_out": map[string]any{"videos": 4, "within_3": 0.5, "within_10": 0.75, "within_30": within30, "median": 2.5},
	})
	return string(data)
}

// A video with a reading under a root, and the marks of its folder.
func seedReadVideo(t *testing.T, root, video, marks string) {
	t.Helper()
	folder := filepath.Join(root, filepath.Dir(video))
	writeFile(t, filepath.Join(root, video), "video")
	writeFile(t, filepath.Join(folder, likenDirectory, titlesRecordsDirectory, filepath.Base(video)+".jsonl"), "{}\n")
	if marks != "" {
		writeFile(t, filepath.Join(folder, likenDirectory, likenLedgerName(factMarks)), marks)
	}
}

func marksOf(file, kind, source string, start int) string {
	return "marks:\n    - path: " + file + "\n      kind: " + kind + "\n      start: " +
		itoa(int64(start)) + "\n      source: " + source + "\n"
}

// A library of three films: one with marks from both providers, one with a
// mark from IntroDB alone, and one with a reading and no mark.
func seedFitLibrary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	both := marksOf("a.mkv", markKindCredits, "introdb", 5000000) +
		"    - path: a.mkv\n      kind: credits\n      start: 5400000\n      source: theintrodb\n"
	seedReadVideo(t, root, "A (2001)/a.mkv", both)
	seedReadVideo(t, root, "B (2002)/b.mkv", marksOf("b.mkv", markKindCredits, "introdb", 6100500))
	seedReadVideo(t, root, "C (2003)/c.mkv", marksOf("c.mkv", markKindIntro, "theintrodb", 1000))
	// A partial reading that a killed read left is no reading.
	writeFile(t, filepath.Join(root, "B (2002)", likenDirectory, titlesRecordsDirectory, "x.mkv.jsonl.partial-host-41"), "{}")
	return root
}

func testTitlesFit(t *testing.T, root string) (*titlesFitRun, *retainBroker, *bytes.Buffer) {
	t.Helper()
	broker := newRetainBroker(t)
	log := &bytes.Buffer{}
	return &titlesFitRun{namespace: "house", name: "movies", kind: libraryKindMovies, root: root,
		job: "movies-titles-fit-1", writer: newVolumeWriter("movies-titles-fit-1"), log: log, base: defaultTopicBase, dial: broker.dial, scratch: t.TempDir()}, broker, log
}

func publishedFit(t *testing.T, broker *retainBroker) titlesFit {
	t.Helper()
	payload, held := broker.retainedOn(titlesFitTopic(defaultTopicBase, "house", "movies"))
	if !held {
		t.Fatal("the broker holds no fit")
	}
	var fit titlesFit
	if err := json.Unmarshal(payload, &fit); err != nil {
		t.Fatal(err)
	}
	return fit
}

// A fit from start to end: the labels are the marked videos with a reading,
// with TheIntroDB's mark where both providers have one, the model takes its
// name under the root, and the result on the bus holds the counts and the
// score.
func TestAFitLandsTheModelAndPublishesItsScore(t *testing.T) {
	root := seedFitLibrary(t)
	tool := installStandInTrain(t, trainedModel(0.8))
	run, broker, log := testTitlesFit(t, root)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	if err := run.fit(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	labels, _ := os.ReadFile(filepath.Join(tool, "labels.tsv"))
	wantLabels := filepath.Join(root, "A (2001)/a.mkv") + "\t5400.000\t" + filepath.Join(root, "A (2001)") + "\n" +
		filepath.Join(root, "B (2002)/b.mkv") + "\t6100.500\t" + filepath.Join(root, "B (2002)") + "\n"
	if string(labels) != wantLabels {
		t.Errorf("labels = %q, want %q", labels, wantLabels)
	}
	directory := filepath.Join(root, likenDirectory, titlesRecordsDirectory)
	if left := namesIn(t, directory); len(left) != 1 || left[0] != titlesModelName {
		t.Errorf("the model's directory holds %v, want the model alone", left)
	}
	want := titlesFit{Kind: titlesKindEnd, Result: titlesFitFitted, Job: "movies-titles-fit-1", At: now,
		Marked: titlesMarked{Videos: 2, Groups: 2}, Videos: 3, LeftOut: 1,
		HeldOut: 4, Within3: 0.5, Within10: 0.75, Within30: 0.8, Median: 2.5}
	if fit := publishedFit(t, broker); fit != want {
		t.Errorf("fit = %+v, want %+v", fit, want)
	}
	if strings.Contains(log.String(), "A (2001)") {
		t.Errorf("log = %q, want no title", log)
	}
}

// A model under the floor, and a fit the tool fails, leave the model the
// Library has and say why on the bus.
func TestAFitThatGivesNoModelLeavesTheOldOne(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		result string
		reason string
	}{
		{name: "under the floor", model: trainedModel(0.2), result: titlesFitRejected, reason: "under the floor of 30%"},
		{name: "the tool fails", result: titlesFitFailed, reason: "too few videos have a reading"},
		{name: "the tool writes no JSON", model: "model", result: titlesFitFailed, reason: "reading the model the fit wrote"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := seedFitLibrary(t)
			directory := filepath.Join(root, likenDirectory, titlesRecordsDirectory)
			writeFile(t, filepath.Join(directory, titlesModelName), "the model before")
			// What a fit that was killed left.
			writeFile(t, filepath.Join(directory, titlesModelName+likenTempMark+"movies-titles-fit-0"), "half a model")
			installStandInTrain(t, test.model)
			run, broker, _ := testTitlesFit(t, root)

			if err := run.fit(t.Context(), time.Now()); err != nil {
				t.Fatal(err)
			}

			fit := publishedFit(t, broker)
			if fit.Result != test.result || !strings.Contains(fit.Reason, test.reason) {
				t.Errorf("fit = %+v, want %s with the reason %q", fit, test.result, test.reason)
			}
			if held, _ := os.ReadFile(filepath.Join(directory, titlesModelName)); string(held) != "the model before" {
				t.Errorf("the model is %q, want the model before", held)
			}
			if left := namesIn(t, directory); len(left) != 1 {
				t.Errorf("the model's directory holds %v, want no candidate", left)
			}
		})
	}
}

// A Job that is deleted stops the fit in the middle. The run publishes
// nothing, so the next Job fits.
func TestAStoppedFitPublishesNothing(t *testing.T) {
	root := seedFitLibrary(t)
	installStandInTrain(t, trainedModel(0.8))
	run, broker, _ := testTitlesFit(t, root)
	ctx, stop := context.WithCancel(t.Context())
	stop()

	if err := run.fit(ctx, time.Now()); err == nil {
		t.Error("fit = nil error, want the stop")
	}

	if _, held := broker.retainedOn(titlesFitTopic(defaultTopicBase, "house", "movies")); held {
		t.Error("the broker holds a fit, want none")
	}
}

// A fit that cannot reach the broker fails, so the Job runs it again.
func TestAFitThatCannotPublishFails(t *testing.T) {
	root := seedFitLibrary(t)
	installStandInTrain(t, trainedModel(0.8))
	run, _, _ := testTitlesFit(t, root)
	run.dial = func(context.Context) (net.Conn, error) { return nil, errors.New("no broker") }

	if err := run.fit(t.Context(), time.Now()); err == nil || !strings.Contains(err.Error(), "no broker") {
		t.Errorf("fit = %v, want the broker's error", err)
	}
}

// A marks ledger that cannot be read gives its videos no label.
func TestAVideoWithAnUnreadableLedgerHasNoLabel(t *testing.T) {
	root := t.TempDir()
	seedReadVideo(t, root, "A (2001)/a.mkv", "marks: [")
	run, _, _ := testTitlesFit(t, root)

	labels, err := run.labels()

	if err != nil || len(labels) != 0 {
		t.Errorf("labels = %+v (%v), want none", labels, err)
	}
}

// The episodes of one series are one group, whatever their seasons.
func TestTheEpisodesOfASeriesAreOneGroup(t *testing.T) {
	root := t.TempDir()
	seedReadVideo(t, root, "Show/Season 01/s01e01.mkv", marksOf("s01e01.mkv", markKindCredits, "introdb", 1300000))
	seedReadVideo(t, root, "Show/Season 02/s02e01.mkv", marksOf("s02e01.mkv", markKindCredits, "introdb", 1310000))
	run, _, _ := testTitlesFit(t, root)
	run.kind = libraryKindSeries

	labels, err := run.labels()
	if err != nil {
		t.Fatal(err)
	}

	if marked := countLabels(labels); marked != (titlesMarked{Videos: 2, Groups: 1}) {
		t.Errorf("marked = %+v from %+v, want two videos of one series", marked, labels)
	}
}

// A run with no log writes nowhere, and a root that is gone fails the run.
func TestAFitOfARootThatIsGoneFails(t *testing.T) {
	run, _, _ := testTitlesFit(t, filepath.Join(t.TempDir(), "gone"))
	run.log = nil
	run.logf("nothing")

	if err := run.fit(t.Context(), time.Now()); err == nil {
		t.Error("fit = nil error, want the missing root's")
	}
}
