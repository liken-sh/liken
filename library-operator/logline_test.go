package main

// These tests read the operator's own log: the helpers that keep library
// content out of a line, and the writer every human-scale line goes through.

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// An id that carries a folder key or a name carries a title, so its tail is
// hashed. An id that carries a provider's number passes through, because a
// number names nothing a person reads.
func TestAnIDThatCarriesATitleIsHashed(t *testing.T) {
	cases := []struct {
		id, want string
	}{
		{"movie:tmdb:1001", "movie:tmdb:1001"},
		{"episode:tvdb:2002:s02e05", "episode:tvdb:2002:s02e05"},
		{"movie:path:Some Film (1999)", "movie:path:" + hashed("Some Film (1999)")},
		{"episode:path:Some Show:s01e02", "episode:path:" + hashed("Some Show:s01e02")},
		{"set:name:some-set", "set:name:" + hashed("some-set")},
		{"franchise:name:some-story", "franchise:name:" + hashed("some-story")},
		{"movies:1", "movies:1"},
		{"", ""},
	}
	for _, c := range cases {
		if got := opaqueID(c.id); got != c.want {
			t.Errorf("opaqueID(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}

// The hash is the first 12 hexadecimal characters of the SHA-256 of the
// tail, which the media browser computes the same way, so one id reads the
// same in both logs.
func TestTheHashIsTheFirstTwelveHexCharactersOfTheSHA256(t *testing.T) {
	if got := opaqueID("movie:path:abc"); got != "movie:path:ba7816bf8f01" {
		t.Errorf("opaqueID = %q, want the SHA-256 of abc cut to 12 characters", got)
	}
}

// A work is named by its provider ids in provider order, with a folder key
// hashed, and an episode by its numbers after them.
func TestAWorkIsNamedByItsProviderIDs(t *testing.T) {
	cases := []struct {
		name            string
		aliases         map[string]string
		season, episode int
		want            string
	}{
		{"a movie", map[string]string{"tmdb": "1001", "imdb": "tt0001"}, 0, 0, "imdb:tt0001 tmdb:1001"},
		{"an episode", map[string]string{"tvdb": "2002"}, 2, 5, "tvdb:2002 s02e05"},
		{"a folder key", map[string]string{"path": "some-film-1999"}, 0, 0, "path:" + hashed("some-film-1999")},
		{"no ids", nil, 0, 0, "a work with no ids"},
	}
	for _, c := range cases {
		if got := workNamed(c.aliases, c.season, c.episode); got != c.want {
			t.Errorf("%s: workNamed = %q, want %q", c.name, got, c.want)
		}
	}
}

// Lines from the pass, the bus reader, and the webhook server arrive at one
// writer at once, and each one lands whole on a line of its own.
func TestLinesFromManyGoroutinesLandWhole(t *testing.T) {
	operator, logged := loggingOperator(t, newFakeCluster())
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() { operator.logf("one line of %d words", 5) })
	}
	group.Wait()

	lines := strings.Split(strings.TrimSuffix(logged.String(), "\n"), "\n")
	if len(lines) != 20 {
		t.Fatalf("lines = %d, want 20", len(lines))
	}
	for _, line := range lines {
		if line != "library.liken.sh: one line of 5 words" {
			t.Errorf("line = %q", line)
		}
	}
}

// An operator built with no log writes nothing and does not fail.
func TestAnOperatorWithNoLogWritesNothing(t *testing.T) {
	operator := testOperator(t, newFakeCluster())
	operator.log = nil
	operator.logf("a line nobody reads")
}

// The operator of testOperator with its log in a buffer the test reads.
func loggingOperator(t *testing.T, cluster *fakeCluster) (*operator, *bytes.Buffer) {
	t.Helper()
	operator := testOperator(t, cluster)
	logged := &bytes.Buffer{}
	operator.log = logged
	return operator, logged
}

// The lines of a log that hold every one of these words, so a test counts a
// line by its facts and not by its whole wording.
func linesWith(logged *bytes.Buffer, words ...string) []string {
	var found []string
	for line := range strings.SplitSeq(logged.String(), "\n") {
		held := line != ""
		for _, word := range words {
			held = held && strings.Contains(line, word)
		}
		if held {
			found = append(found, line)
		}
	}
	return found
}

// The test's own count of one line: exactly one line holds the words.
func wantOneLine(t *testing.T, logged *bytes.Buffer, words ...string) {
	t.Helper()
	if found := linesWith(logged, words...); len(found) != 1 {
		t.Errorf("lines with %q = %q, want one in:\n%s", words, found, logged.String())
	}
}

func TestACountCarriesItsNoun(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{{0, "0 folders"}, {1, "1 folder"}, {2, "2 folders"}}
	for _, c := range cases {
		if got := counted(c.count, "folder"); got != c.want {
			t.Errorf("counted(%d) = %q, want %q", c.count, got, c.want)
		}
	}
}
