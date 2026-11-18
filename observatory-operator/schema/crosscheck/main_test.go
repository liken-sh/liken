package main

import "testing"

// The report records the commit it read, so the URL must point at the
// page at that commit and not at the newest version.
func TestThePageIsReadAtTheCommitNamed(t *testing.T) {
	want := "https://raw.githubusercontent.com/indilib/docs/a1d5584e/drivers/standard-properties.md"
	if got := pageURL("a1d5584e"); got != want {
		t.Errorf("pageURL = %q, want %q", got, want)
	}
}
