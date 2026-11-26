package main

// probebacklog_test.go proves how the probe works through a backlog, such as
// the read of every video again when it gains a new fact: it stops starting
// files at the phase's time limit, so the Job closes and the next walk goes
// on with the gap; it reads a new or changed file before a file it reads
// again only for a new fact; and a file read again keeps the measurements its
// record already has at their current version.

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// The probe is a time-limited phase, so a walk's Job ends before its
// deadline however long the probe's gap is.
func TestTheProbeIsATimeLimitedPhase(t *testing.T) {
	if !timeLimitedPhases[factProbe] {
		t.Error("the probe phase has no time limit")
	}
}

// A probe that takes ten minutes for each file starts the first two files of
// three before a limit of fifteen minutes, finishes the second, and leaves
// the third in the gap with no attempt.
func TestTheProbeStartsNoFileAfterItsTimeLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		root := t.TempDir()
		for _, folder := range []string{"A Survey (1981)", "B Survey (1982)", "C Survey (1983)"} {
			seedProbeGap(t, catalog, root, folder, folder+".mkv")
		}
		work, _ := testEnricher(t, libraryKindMovies, root, catalog)
		work.stopStarting = time.Now().Add(15 * time.Minute)
		var read []string
		slow := func(_ context.Context, path string) ([]byte, error) {
			read = append(read, filepath.Base(path))
			time.Sleep(10 * time.Minute)
			return []byte(ffprobeOfOneFile), nil
		}

		if err := work.probeGap(t.Context(), slow); err != nil {
			t.Fatal(err)
		}

		if want := []string{"A Survey (1981).mkv", "B Survey (1982).mkv"}; !slices.Equal(read, want) {
			t.Errorf("read %v, want %v", read, want)
		}
		ledger, err := readLikenLedger(filepath.Join(root, "C Survey (1983)"), factProbe)
		if err != nil {
			t.Fatal(err)
		}
		if len(ledger.Attempts) != 0 {
			t.Errorf("attempts of the third file = %+v, want none", ledger.Attempts)
		}
	})
}

// A file whose record describes it and lacks only a new fact is read after
// a file that no probe has read, so a new download does not wait behind the
// backlog of a new fact.
func TestTheProbeReadsANewFileBeforeAFileItReadsAgain(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "A Survey (1981)", "A Survey (1981).mkv")
	seedProbeGap(t, catalog, root, "B Survey (1982)", "B Survey (1982).mkv")
	again := filepath.Join(root, "A Survey (1981)", "A Survey (1981).mkv")
	writeProbeLedger(t, filepath.Dir(again), probedFile{
		Path: "A Survey (1981).mkv", Modified: modifiedOf(t, again), Size: int64(len("video")),
		Duration: 1500, Counted: true, Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264"}},
	})
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	var read []string
	probe := func(_ context.Context, path string) ([]byte, error) {
		read = append(read, filepath.Base(path))
		return []byte(ffprobeOfOneFile), nil
	}

	if err := work.probeGap(t.Context(), probe); err != nil {
		t.Fatal(err)
	}

	if want := []string{"B Survey (1982).mkv", "A Survey (1981).mkv"}; !slices.Equal(read, want) {
		t.Errorf("read %v, want %v", read, want)
	}
}

// The probe record of the one film of a test, written before the probe
// measured active areas, with a PGS stream that the current measurement of
// plan 97 measured.
func heldFilmRecord(t *testing.T, root string) {
	t.Helper()
	film := filepath.Join(root, "The Long Survey (1982)", "The Long Survey (1982).mkv")
	writeProbeLedger(t, filepath.Dir(film), probedFile{
		Path: "The Long Survey (1982).mkv", Modified: modifiedOf(t, film), Size: int64(len("video")),
		Duration: 4000, Counted: true, Measured: true, Measurement: imageMeasurement,
		Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264"},
			{Kind: fileTypeSubtitle, Codec: codecPGS, Image: filmImage}},
	})
}

// A file read again for its active area keeps the image measurement of its
// record, which is at the current version, and reads no subtitle window. A
// window that would fail cannot take the measurement away.
func TestAFileReadAgainKeepsItsCurrentImageMeasurement(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Long Survey (1982)", "The Long Survey (1982).mkv")
	heldFilmRecord(t, root)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	windows := &fakeWindows{err: errors.New("ffprobe: exit status 1")}
	answeringWindows(t, windows)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfAFilmWithAPGSStream)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, "The Long Survey (1982)"), factProbe)
	if err != nil {
		t.Fatal(err)
	}
	record, _ := ledger.probeAt("The Long Survey (1982).mkv")
	if len(windows.starts) != 0 || record.Streams[1].Image == nil || *record.Streams[1].Image != *filmImage ||
		record.Measurement != imageMeasurement || record.ActiveAreaMeasurement != activeAreaMeasurement {
		t.Errorf("windows read at %v and record %+v, want no window, the held numbers, and both versions",
			windows.starts, record)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempts = %+v, want one found attempt", ledger.Attempts)
	}
}

// A held record keeps its measurements only for the file it describes, with
// the same streams. Any other record is measured again.
func TestAMeasurementIsKeptOnlyForTheFileAndStreamsItDescribes(t *testing.T) {
	read := probedFile{Size: 5, Modified: 100, Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264"},
		{Kind: fileTypeSubtitle, Codec: codecPGS}}}
	held := func(change func(*probedFile)) probedFile {
		record := probedFile{Size: 5, Modified: 100, Measured: true, Measurement: imageMeasurement,
			ActiveAreaMeasurement: activeAreaMeasurement,
			Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264", ActiveArea: scopeArea},
				{Kind: fileTypeSubtitle, Codec: codecPGS, Image: filmImage}}}
		change(&record)
		return record
	}
	cases := []struct {
		name          string
		held          probedFile
		images, areas bool
	}{
		{name: "the same file", held: held(func(*probedFile) {}), images: true, areas: true},
		{name: "an earlier image measurement", held: held(func(r *probedFile) { r.Measurement = 0 }), areas: true},
		{name: "no active area measurement", held: held(func(r *probedFile) { r.ActiveAreaMeasurement = 0 }), images: true},
		{name: "another size", held: held(func(r *probedFile) { r.Size = 6 })},
		{name: "another modified time", held: held(func(r *probedFile) { r.Modified = 99 })},
		{name: "other streams", held: held(func(r *probedFile) { r.Streams[1].Codec = codecVobSub })},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			record := read
			record.Streams = slices.Clone(read.Streams)
			images, areas := record.keepCurrentMeasurements(test.held, true)
			if images != test.images || areas != test.areas {
				t.Errorf("kept images %v and areas %v, want %v and %v", images, areas, test.images, test.areas)
			}
			if test.images && (record.Streams[1].Image == nil || !record.Measured) {
				t.Errorf("record = %+v, want the held image block", record)
			}
			if test.areas && (record.Streams[0].ActiveArea == nil || record.ActiveAreaMeasurement != activeAreaMeasurement) {
				t.Errorf("record = %+v, want the held area", record)
			}
		})
	}
}

// A sample that fails makes the attempt an error and leaves the record
// without the version, so the probe reads the file again after the error's
// period.
func TestAFailedSampleRecordsAnErrorAttempt(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Long Survey (1982)", "The Long Survey (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	samples := &fakeSamples{err: errors.New("ffmpeg: exit status 1")}
	was := readActiveAreaSample
	t.Cleanup(func() { readActiveAreaSample = was })
	readActiveAreaSample = samples.read

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, "The Long Survey (1982)"), factProbe)
	if err != nil {
		t.Fatal(err)
	}
	record, held := ledger.probeAt("The Long Survey (1982).mkv")
	if !held || record.ActiveAreaMeasurement != 0 || record.first(fileTypeVideo).Codec != "h264" {
		t.Errorf("record = %+v, want the header's streams and no version", record)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want one error attempt", ledger.Attempts)
	}
}
