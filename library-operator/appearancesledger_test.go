package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The appearances ledger as a person reads it: one line per face, per person,
// and per model, and the same entry read back.

// One entry with a face named over a runner-up, a face named in a gallery of
// one, and a person with no headshot.
func sampleAppearances() appearancesEntry {
	return appearancesEntry{
		Path: appearancesFile, Size: 4831838208,
		Embedder:  detectionsModel{Name: "face_recognition_sface_2021dec", SHA256: "0ba9fbfa"},
		Threshold: 0.363, Margin: 0.05,
		Gallery: []appearancesPerson{
			{Contributor: ".contributors/ad/ada-quill", Name: "Ada Quill", Headshot: "found", SHA256: "ab12"},
			{Contributor: ".contributors/bo/bo-reyes", Name: "Bo Reyes", Headshot: "missing"},
		},
		Unmatched: []appearancesPerson{{Contributor: ".contributors/bo/bo-reyes", Name: "Bo Reyes", Headshot: "missing"}},
		Named:     appearancesNamed{Headshot: 4210, Film: 1037}, People: 12,
	}
}

func TestTheAppearancesLedgerHoldsTheCountsAndNoFace(t *testing.T) {
	folder := t.TempDir()
	writer := newVolumeWriter("movies-appearances")

	err := writer.updateLikenLedger(folder, factAppearances, func(ledger *likenLedger) {
		ledger.noteAppearances(sampleAppearances())
	})

	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(folder, likenDirectory, "appearances.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, line := range []string{
		"embedder: {name: face_recognition_sface_2021dec, sha256: 0ba9fbfa}",
		"- {contributor: .contributors/bo/bo-reyes, name: Bo Reyes, headshot: missing}",
		"named: {headshot: 4210, film: 1037}",
		"people: 12",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("ledger holds no line %q in:\n%s", line, text)
		}
	}
	if strings.Contains(text, "observations") {
		t.Errorf("ledger holds observations:\n%s", text)
	}
	read := appearancesLedgerOf(t, folder)
	if len(read.Appearances) != 1 || !sameAppearances(read.Appearances[0], sampleAppearances()) {
		t.Errorf("read back %+v, want %+v", read.Appearances, sampleAppearances())
	}
}

// A second answer for one file replaces the first, and an answer for another
// file joins it.
func TestOneFileHoldsOneAppearancesEntry(t *testing.T) {
	ledger := likenLedger{}
	first := sampleAppearances()
	second := sampleAppearances()
	second.Named = appearancesNamed{}
	other := sampleAppearances()
	other.Path = appearancesEpisode

	for _, entry := range []appearancesEntry{first, other, second} {
		ledger.noteAppearances(entry)
	}

	if len(ledger.Appearances) != 2 || ledger.Appearances[0].Named != (appearancesNamed{}) ||
		ledger.Appearances[1].Path != appearancesEpisode {
		t.Errorf("appearances = %+v, want the second answer for the file and the other file's", ledger.Appearances)
	}
}
