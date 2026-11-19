package machine

import (
	"strings"
	"testing"
)

func TestParseChannelReadsTheLatestVersion(t *testing.T) {
	doc := `apiVersion: liken.sh/v1alpha1
kind: Channel
metadata:
  name: liken
latest: 2026.07.13-002
`
	c, err := ParseChannel([]byte(doc))
	if err != nil {
		t.Fatalf("parsing the Channel: %v", err)
	}
	if c.Metadata.Name != "liken" {
		t.Errorf("the name is %q, want liken", c.Metadata.Name)
	}
	if c.Latest != "2026.07.13-002" {
		t.Errorf("latest is %q, want 2026.07.13-002", c.Latest)
	}
}

func TestParseChannelRejectsTheWrongKind(t *testing.T) {
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Release\nmetadata:\n  name: liken\nlatest: 2026.07.13-002\n"
	if _, err := ParseChannel([]byte(doc)); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Errorf("parsing a Release document as a Channel returned %v, want an error about the kind", err)
	}
}

func TestParseChannelRejectsAMissingName(t *testing.T) {
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Channel\nmetadata: {}\nlatest: 2026.07.13-002\n"
	if _, err := ParseChannel([]byte(doc)); err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("parsing a Channel with no name returned %v, want an error about the name", err)
	}
}

func TestParseChannelRejectsAMalformedLatest(t *testing.T) {
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Channel\nmetadata:\n  name: liken\nlatest: not-a-version\n"
	if _, err := ParseChannel([]byte(doc)); err == nil || !strings.Contains(err.Error(), "latest") {
		t.Errorf("parsing a Channel with latest not-a-version returned %v, want an error about latest", err)
	}
}
