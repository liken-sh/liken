package main

import "testing"

// The report names the commit it read, so the address must read the
// page at that commit and not at the newest one.
func TestThePageIsReadAtTheCommitNamed(t *testing.T) {
	want := "https://raw.githubusercontent.com/indilib/docs/a1d5584e/drivers/standard-properties.md"
	if got := pageURL("a1d5584e"); got != want {
		t.Errorf("pageURL = %q, want %q", got, want)
	}
}
