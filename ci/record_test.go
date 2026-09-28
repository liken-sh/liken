package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestTheRecordGivesEachComponentItsVersionAtTheTag(t *testing.T) {
	components := graphFixture(t)
	published := map[string][]string{
		"app":  {"2026.09.27-001", "2026.10.02-001", "2026.10.03-001", "2026.10.02-001-dev-001-abcdef01"},
		"base": {"2026.09.20-001", "latest"},
	}
	entries, err := Record(components, func(c *Component) ([]string, error) { return published[c.Name()], nil }, "2026.10.02-001")
	if err != nil {
		t.Fatal(err)
	}
	want := []RecordEntry{
		{Component: "app", Version: "2026.10.02-001", Released: true},
		{Component: "base", Version: "2026.09.20-001"},
		{Component: "os", Version: ""},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries %+v", entries)
	}
}

func TestTheRecordNotesPutTheCatalogEntryFirst(t *testing.T) {
	notes := RecordNotes("2026.10.02-001", []RecordEntry{
		{Component: "liken", Version: "2026.10.02-001", Released: true},
		{Component: "operator", Version: "2026.09.27-001"},
		{Component: "new", Version: ""},
	}, "sha256:abc", []string{"Change the operator"})
	for _, want := range []string{
		"  - version: 2026.10.02-001\n    digest: sha256:abc\n",
		"| `liken` | `2026.10.02-001` | released by this tag |",
		"| `operator` | `2026.09.27-001` | unchanged |",
		"| `new` | `none` | not published yet |",
		"- Change the operator\n",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("the notes lack %q:\n%s", want, notes)
		}
	}
	if !strings.HasPrefix(notes, "liken 2026.10.02-001 is on the channel") {
		t.Errorf("the notes start:\n%s", notes)
	}
}

func TestAReleaseVersionIsCalVer(t *testing.T) {
	versions := []string{"2026.09.26-001", "latest", "2026.09.27-002", "2026.09.27-002-dev-004-abcdef01", "buildcache", "2026.09.27-001"}
	if got := NewestRelease(versions); got != "2026.09.27-002" {
		t.Errorf("NewestRelease = %s", got)
	}
	if got := NewestReleaseAtOrBefore(versions, "2026.09.27-001"); got != "2026.09.27-001" {
		t.Errorf("NewestReleaseAtOrBefore = %s", got)
	}
	if got := NewestRelease([]string{"latest"}); got != "" {
		t.Errorf("NewestRelease of no release = %s", got)
	}
	if got := DevVersion("2026.10.02-001", 17, "abcdef0123456789"); got != "2026.10.02-001-dev-017-abcdef01" {
		t.Errorf("DevVersion = %s", got)
	}
}
