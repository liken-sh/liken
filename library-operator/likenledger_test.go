package main

import (
	"path/filepath"
	"testing"
	"time"
)

// The one time every ledger test writes, so the files a test compares read
// the same on every run.
var ledgerTime = time.Date(2026, 9, 2, 14, 0, 0, 0, time.UTC)

func TestAFolderWithNoLikenDirectoryReadsAsAnEmptyLedger(t *testing.T) {
	ledger, err := readLikenLedger(t.TempDir(), concernIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Items) != 0 || len(ledger.Attempts) != 0 {
		t.Errorf("read %+v, want an empty ledger", ledger)
	}
}

func TestALedgerThatIsNotYAMLIsAnError(t *testing.T) {
	folder := t.TempDir()
	writeFile(t, filepath.Join(folder, likenDirectory, "identity.yaml"), "items: [oh: {: no\n")

	if _, err := readLikenLedger(folder, concernIdentity); err == nil {
		t.Error("the read reported no error, want one")
	}
}

func TestAProbeAttemptIsWrittenInThePlansShape(t *testing.T) {
	folder := t.TempDir()

	err := newVolumeWriter("movies-enrich").updateLikenLedger(folder, concernProbe, func(ledger *likenLedger) {
		ledger.noteAttempt(likenAttempt{Path: "The Thing (1982).mkv", At: ledgerTime, Result: attemptFound})
	})
	if err != nil {
		t.Fatal(err)
	}

	want := "attempts:\n    - path: The Thing (1982).mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n"
	if got := readFileString(t, filepath.Join(folder, likenDirectory, "probe.yaml")); got != want {
		t.Errorf("wrote\n%q\nwant\n%q", got, want)
	}
}

func TestAnIdentityLedgerHoldsAnIdAndItsReason(t *testing.T) {
	folder := t.TempDir()

	err := newVolumeWriter("movies-enrich").updateLikenLedger(folder, concernIdentity, func(ledger *likenLedger) {
		ledger.noteItem(likenItem{Path: likenSelfPath, ID: providerIDs{"tmdb": "603"}, Reason: reasonFrom(testTitle, testYear), Written: ledgerTime})
		ledger.noteAttempt(likenAttempt{Path: likenSelfPath, At: ledgerTime, Result: attemptFound})
	})
	if err != nil {
		t.Fatal(err)
	}

	got := readFileString(t, filepath.Join(folder, likenDirectory, "identity.yaml"))
	want := "items:\n    - path: .\n      id: {tmdb: 603}\n      reason: title and year\n" +
		"      written: 2026-09-02T14:00:00Z\nattempts:\n    - path: .\n      at: 2026-09-02T14:00:00Z\n      result: found\n"
	if got != want {
		t.Errorf("wrote\n%q\nwant\n%q", got, want)
	}
}

func TestAnIdentityLedgerHoldsCandidatesWithTheirReceipts(t *testing.T) {
	folder := t.TempDir()

	err := newVolumeWriter("movies-enrich").updateLikenLedger(folder, concernIdentity, func(ledger *likenLedger) {
		ledger.noteItem(likenItem{Path: likenSelfPath, Candidates: []likenCandidate{
			{ID: providerIDs{"tmdb": "11"}, Title: "Star Wars", Year: 1977, Receipt: map[string]string{"title": "match"}},
		}})
	})
	if err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(folder, concernIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Items) != 1 || len(ledger.Items[0].Candidates) != 1 {
		t.Fatalf("read %+v, want one item with one candidate", ledger)
	}
	if got := ledger.Items[0].Candidates[0]; got.ID["tmdb"] != "11" || got.Receipt["title"] != "match" {
		t.Errorf("candidate = %+v, want the id and the receipt", got)
	}
}

func TestALaterEntryReplacesTheOneForItsOwnPath(t *testing.T) {
	folder := t.TempDir()
	writer := newVolumeWriter("movies-enrich")

	for _, result := range []string{attemptError, attemptFound} {
		err := writer.updateLikenLedger(folder, concernProbe, func(ledger *likenLedger) {
			ledger.noteAttempt(likenAttempt{Path: "a.mkv", At: ledgerTime, Result: result})
			ledger.noteAttempt(likenAttempt{Path: "b.mkv", At: ledgerTime, Result: attemptFound})
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	ledger, err := readLikenLedger(folder, concernProbe)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 2 {
		t.Fatalf("the ledger holds %d attempts, want one per path", len(ledger.Attempts))
	}
	if ledger.Attempts[0].Result != attemptFound {
		t.Errorf("the first attempt is %q, want the second run's result", ledger.Attempts[0].Result)
	}
}

func TestAnIdentityItemReplacesTheAnswerBeforeIt(t *testing.T) {
	folder := t.TempDir()
	writer := newVolumeWriter("movies-enrich")

	for _, reason := range []string{"", reasonFrom(testTitle, testYear)} {
		err := writer.updateLikenLedger(folder, concernIdentity, func(ledger *likenLedger) {
			ledger.noteItem(likenItem{Path: likenSelfPath, Reason: reason})
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	ledger, err := readLikenLedger(folder, concernIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Items) != 1 || ledger.Items[0].Reason != reasonFrom(testTitle, testYear) {
		t.Errorf("read %+v, want one item with the second reason", ledger.Items)
	}
}

func TestAnIdReadsBackFromANumberOrAString(t *testing.T) {
	cases := []struct {
		name     string
		document string
		want     string
	}{
		{name: "a number, as the ledger writes it", document: "items:\n  - path: .\n    id: {tmdb: 603}\n", want: "603"},
		{name: "a string, as a person may write it", document: "items:\n  - path: .\n    id: {tmdb: \"603\"}\n", want: "603"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			folder := t.TempDir()
			writeFile(t, filepath.Join(folder, likenDirectory, "identity.yaml"), test.document)

			ledger, err := readLikenLedger(folder, concernIdentity)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Items) != 1 || ledger.Items[0].ID["tmdb"] != test.want {
				t.Errorf("read %+v, want the id %s", ledger.Items, test.want)
			}
		})
	}
}
