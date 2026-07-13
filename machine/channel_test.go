package machine

// Tests for the channel document: the advisory "what's the latest?"
// announcement at a channel's root.

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
		t.Fatal(err)
	}
	if c.Metadata.Name != "liken" {
		t.Errorf("name: %q", c.Metadata.Name)
	}
	if c.Latest != "2026.07.13-002" {
		t.Errorf("latest: %q", c.Latest)
	}
}

func TestParseChannelRejectsTheWrongKind(t *testing.T) {
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Release\nmetadata:\n  name: liken\nlatest: 2026.07.13-002\n"
	if _, err := ParseChannel([]byte(doc)); err == nil || !strings.Contains(err.Error(), "kind Channel") {
		t.Errorf("a Release document must not parse as a Channel: %v", err)
	}
}

func TestParseChannelRejectsAMissingName(t *testing.T) {
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Channel\nmetadata: {}\nlatest: 2026.07.13-002\n"
	if _, err := ParseChannel([]byte(doc)); err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("a nameless channel must be rejected: %v", err)
	}
}

func TestParseChannelRejectsAMalformedLatest(t *testing.T) {
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Channel\nmetadata:\n  name: liken\nlatest: not-a-version\n"
	if _, err := ParseChannel([]byte(doc)); err == nil || !strings.Contains(err.Error(), "latest") {
		t.Errorf("a malformed latest must be rejected: %v", err)
	}
}
