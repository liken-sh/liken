package main

// What these tests read: the credits move leaves a title's credits as they
// are outside a narrowed Job's scope and where the folder refuses the write.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Job narrowed to another folder, as a webhook's rescan is, moves no
// credit of this title and records no attempt, so the title stays in the gap.
func TestANarrowedJobMovesNoCreditOutsideItsScope(t *testing.T) {
	work, _, folder := titleCreditingAMergedEntry(t, []creditEntry{
		{Name: "Oren Talley", Part: creditPartActor, Order: 0, Contributor: ".contributors/or/oren-talley"},
	})
	work.scopes = []string{"Another Film (2015)"}

	if err := work.moveCredits(t.Context()); err != nil {
		t.Fatal(err)
	}

	credits := artLedger(t, folder, factCredits).Credits
	if len(credits) != 1 || credits[0].Contributor != ".contributors/or/oren-talley" {
		t.Errorf("credits = %+v, want the credit as it was", credits)
	}
	if attempts := artLedger(t, folder, factCreditsMove).Attempts; len(attempts) != 0 {
		t.Errorf("attempts = %+v, want none outside the scope", attempts)
	}
}

// A ledger directory that refuses the write keeps the credits as they were,
// and the log names the write's error.
func TestACreditsMoveTheFolderRefusesIsLogged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	work, _, folder := titleCreditingAMergedEntry(t, []creditEntry{
		{Name: "Oren Talley", Part: creditPartActor, Order: 0, Contributor: ".contributors/or/oren-talley"},
	})
	log := &bytes.Buffer{}
	work.log = log
	ledgers := filepath.Join(folder, likenDirectory)
	if err := os.Chmod(ledgers, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ledgers, 0o755) })

	if err := work.moveCredits(t.Context()); err != nil {
		t.Fatal(err)
	}

	credits := artLedger(t, folder, factCredits).Credits
	if len(credits) != 1 || credits[0].Contributor != ".contributors/or/oren-talley" {
		t.Errorf("credits = %+v, want the credit as it was", credits)
	}
	if !strings.Contains(log.String(), "could not write the credits of") {
		t.Errorf("log = %q, want the write's error", log)
	}
}
