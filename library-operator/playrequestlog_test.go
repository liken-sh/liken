package main

// These tests read the line a play request leaves: what the screen asked
// for, and the Play the API server created or the reason there is none.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The house of playingHouse with the operator's log in a buffer.
func loggingHouse(t *testing.T) (*operator, *fakeCluster, *bytes.Buffer) {
	t.Helper()
	operator, cluster := playingHouse(t)
	logged := &bytes.Buffer{}
	operator.log = logged
	return operator, cluster, logged
}

// A request as a screen publishes it for one person and one episode, named
// by ids alone, with a slug and a path that carry a title a line must never
// repeat.
func loggedRequest(start string) []byte {
	payload, err := json.Marshal(playRequest{
		Library: testLibraryKey,
		Slug:    "some-show-s02e05",
		Items:   []playRequestItem{film(testFilmPath)},
		People:  []string{"person-a"},
		Aliases: map[string]string{"tvdb": "2002"},
		Season:  2,
		Episode: 5,
		Start:   start,
	})
	if err != nil {
		panic(err)
	}
	return payload
}

func TestACreatedPlayLeavesOneLine(t *testing.T) {
	operator, cluster, logged := loggingHouse(t)
	seedPerson(cluster, "person-a")
	publishPlay(operator, loggedRequest("0:48:32"))

	operator.pass()

	wantOneLine(t, logged, "play request from player house/den", "tvdb:2002 s02e05",
		"library house/movies", "1 item", "people person-a", "start 0:48:32",
		"created the Play with uid "+mintedSuffix+"-uid")
	assertNoTitle(t, logged)
}

func TestAPlayFromTheStartSaysSo(t *testing.T) {
	operator, _, logged := loggingHouse(t)
	publishPlay(operator, loggedRequest(""))

	operator.pass()

	wantOneLine(t, logged, "play request from player house/den", "start at the beginning", "created the Play")
}

// A refusal and a failed create each leave one line with the reason, and
// the API server's own text where the API server refused.
func TestARequestThatMakesNoPlayLeavesOneLineWithTheReason(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(cluster *fakeCluster)
		reason  string
	}{
		{
			name:    "a library the namespace does not hold",
			arrange: func(c *fakeCluster) { delete(c.libraries, "movies") },
			reason:  "refused the request: namespace house holds no library house/movies",
		},
		{
			name:    "a create the API server refuses",
			arrange: func(c *fakeCluster) { c.refuseCreate = true },
			reason:  "the API server refused the Play: ",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			operator, cluster, logged := loggingHouse(t)
			c.arrange(cluster)
			publishPlay(operator, loggedRequest(""))

			operator.pass()

			wantOneLine(t, logged, "play request from player house/den", c.reason)
			assertNoTitle(t, logged)
		})
	}
}

// A path outside the library refuses the request, and the reason names no
// path, because a path carries a title.
func TestARefusedPathIsNotRepeated(t *testing.T) {
	operator, _, logged := loggingHouse(t)
	publishPlay(operator, filmRequest(film("../Other Film (2001)/Other Film (2001).mkv")))

	operator.pass()

	wantOneLine(t, logged, "refused the request: a path of the request is outside the library")
	if strings.Contains(logged.String(), "Other Film") {
		t.Errorf("the log names the path:\n%s", logged)
	}
}

// A pass with no request adds no line.
func TestAPassWithNoRequestAddsNoLine(t *testing.T) {
	operator, _, logged := loggingHouse(t)
	publishPlay(operator, loggedRequest(""))
	operator.pass()
	before := logged.Len()

	operator.pass()

	if added := logged.String()[before:]; added != "" {
		t.Errorf("the second pass logged %q, want nothing", added)
	}
}

// Nothing a test request carries as a title reaches the log.
func assertNoTitle(t *testing.T, logged interface{ String() string }) {
	t.Helper()
	for _, title := range []string{"Some Film", "some-show", "Some Show"} {
		if strings.Contains(logged.String(), title) {
			t.Errorf("the log holds %q:\n%s", title, logged.String())
		}
	}
}
