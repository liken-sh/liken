package main

// These tests cover the command sidecar's half of plan 90 over mpv's
// socket: when mpv loads a file, the sidecar reads the playlist position
// and the track list, and sets sid to the track the rule chooses.

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// subtitleMPV is a commander that chooses subtitles, driven against a pipe
// the test answers as mpv. It returns mpv's end of the pipe and the lines
// the sidecar writes to it, after the observe requests.
func subtitleMPV(t *testing.T, prefs subtitlePreferences, presentations ...string) (net.Conn, <-chan string) {
	t.Helper()
	return subtitleMPVLogging(t, &logBuffer{}, prefs, presentations...)
}

// subtitleMPVLogging is subtitleMPV with the sidecar's log in the test's
// hands.
func subtitleMPVLogging(t *testing.T, log *logBuffer, prefs subtitlePreferences, presentations ...string) (net.Conn, <-chan string) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })
	lines := readAsync(server)
	c := &commander{subtitles: &prefs, log: log}
	for _, block := range presentations {
		c.presentations = append(c.presentations, json.RawMessage(block))
	}
	driveInBackground(t, c, client)
	skipObserves(t, lines)
	return server, lines
}

// answerQuery reads one get_property request and answers it with data.
func answerQuery(t *testing.T, mpv net.Conn, lines <-chan string, property string, data any) {
	t.Helper()
	var sent mpvCommand
	mustSucceed(t, json.Unmarshal([]byte(waitForLine(t, lines)), &sent))
	mustMatch(t, fmt.Sprint(sent.Command), fmt.Sprint([]any{"get_property", property}))
	writeEvent(t, mpv, fmt.Sprintf(`{"request_id":%d,"error":"success","data":%s}`, sent.RequestID, mustEncode(t, data)))
}

// loadedFile answers the two reads of one choice: the playlist position and
// the track list mpv holds for the file at that position.
func loadedFile(t *testing.T, mpv net.Conn, lines <-chan string, position int, file surveyFile) {
	t.Helper()
	answerQuery(t, mpv, lines, "playlist-pos", position)
	answerQuery(t, mpv, lines, "track-list", append([]mpvTrack{audio("eng")}, file.tracks...))
}

// mustSetSid reads the sidecar's set_property sid and answers it.
func mustSetSid(t *testing.T, mpv net.Conn, lines <-chan string, want any) {
	t.Helper()
	var sent mpvCommand
	mustSucceed(t, json.Unmarshal([]byte(waitForLine(t, lines)), &sent))
	mustMatch(t, fmt.Sprint(sent.Command), fmt.Sprint([]any{"set_property", "sid", want}))
	writeEvent(t, mpv, fmt.Sprintf(`{"request_id":%d,"error":"success"}`, sent.RequestID))
}

// presentationWith is an item's block that carries the counts of a file.
func presentationWith(t *testing.T, file surveyFile) string {
	t.Helper()
	return string(mustEncode(t, Presentation{Subtitles: file.counts}))
}

// mpv sends file-loaded after it has opened each file and chosen its audio,
// so the sidecar chooses the subtitle track for each item from that item's
// own counts. The first item carries no counts, so the short default track
// keeps the full role and wins on its flag. The second carries them, and
// the long track wins.
func TestEachLoadedFileGetsTheSubtitleTrackOfItsOwnCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mpv, lines := subtitleMPV(t, english(subtitlesOn, ""), emptyPresentation, presentationWith(t, hawkeye))
		// The sidecar reads mpv's position when it connects, and mpv has no
		// file open yet.
		answerQuery(t, mpv, lines, "playlist-pos", -1)
		mustNoLine(t, lines)

		writeEvent(t, mpv, `{"event":"file-loaded"}`)
		loadedFile(t, mpv, lines, 0, hawkeye)
		mustSetSid(t, mpv, lines, 1)

		writeEvent(t, mpv, `{"event":"file-loaded"}`)
		loadedFile(t, mpv, lines, 1, hawkeye)
		mustSetSid(t, mpv, lines, 3)
	})
}

// The sidecar dials mpv's socket once a second, so mpv can load the first
// file before the sidecar connects, and that file-loaded event reaches
// nobody. So the sidecar chooses for the file mpv has open when it connects.
func TestAFileOpenWhenTheSidecarConnectsGetsItsSubtitleTrack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := &logBuffer{}
		mpv, lines := subtitleMPVLogging(t, log, english(subtitlesOn, ""), presentationWith(t, pluribus))

		loadedFile(t, mpv, lines, 0, pluribus)
		mustSetSid(t, mpv, lines, 2)
		synctest.Wait()

		mustMatchAll(t, log.lines(), []string{
			"command: connected to mpv's socket",
			`command: the sidecar connected to mpv's socket, so the sidecar chose subtitle track 2 (eng, full, 569 events) for item 1, ` +
				`because it is the first full or SDH track in en, sent ["set_property","sid",2] to mpv, mpv answered success`,
		})
	})
}

// In auto, audio in the first language with no forced track shows no
// subtitles, so the sidecar sets sid to no.
func TestAFileWithNoTrackToShowGetsNoSubtitles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mpv, lines := subtitleMPV(t, english(subtitlesAuto, ""), presentationWith(t, horrorStory))

		loadedFile(t, mpv, lines, 0, horrorStory)
		mustSetSid(t, mpv, lines, "no")
	})
}

// The sidecar chooses at load and at no other time, so a track a person
// selects by hand stays until the next file.
func TestATrackAPersonSelectsStays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mpv, lines := subtitleMPV(t, english(subtitlesOn, ""), presentationWith(t, pluribus))
		loadedFile(t, mpv, lines, 0, pluribus)
		mustSetSid(t, mpv, lines, 2)

		writeEvent(t, mpv, `{"event":"property-change","id":6,"name":"current-tracks/sub/lang","data":"ger"}`)
		mustNoLine(t, lines)
	})
}

// A household that names no language gives the rule nothing to choose by, so
// the sidecar sets nothing and logs why.
func TestNoLanguageKeepsMpvsChoice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := &logBuffer{}
		mpv, lines := subtitleMPVLogging(t, log, subtitlePreferences{Subtitles: subtitlesOn}, presentationWith(t, pluribus))

		loadedFile(t, mpv, lines, 0, pluribus)
		mustNoLine(t, lines)

		mustMatchAll(t, log.lines(), []string{
			"command: connected to mpv's socket",
			"command: the sidecar connected to mpv's socket, but the subtitle track of item 1 stays mpv's own choice, " +
				"because neither subtitleLanguages nor audioLanguages names a language",
		})
	})
}

// A file with no subtitle track gives the rule nothing to choose among, so
// the sidecar sends nothing.
func TestAFileWithNoSubtitleTrackGetsNoCommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mpv, lines := subtitleMPV(t, english(subtitlesOn, ""))

		loadedFile(t, mpv, lines, 0, surveyFile{})
		mustNoLine(t, lines)
	})
}

// A read that mpv refuses ends the choice with a line that holds mpv's own
// words, and mpv keeps the track it chose itself.
func TestARefusedReadLogsMpvsWords(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		t.Cleanup(func() { server.Close() })
		lines := readAsync(server)
		log := &logBuffer{}
		prefs := english(subtitlesOn, "")
		c := &commander{subtitles: &prefs, log: log}
		driveInBackground(t, c, client)
		skipObserves(t, lines)

		var sent mpvCommand
		mustSucceed(t, json.Unmarshal([]byte(waitForLine(t, lines)), &sent))
		writeEvent(t, server, fmt.Sprintf(`{"request_id":%d,"error":"property unavailable"}`, sent.RequestID))
		mustNoLine(t, lines)

		mustMatchAll(t, log.lines(), []string{
			"command: connected to mpv's socket",
			"command: the sidecar connected to mpv's socket, but the subtitle track stays mpv's own choice, because the read of playlist-pos failed: mpv answered property unavailable",
		})
	})
}

// A read that mpv never answers, and a read whose socket closes first, each
// end the choice with a line that says why, and mpv keeps its own choice.
func TestAReadWithNoAnswerGivesUp(t *testing.T) {
	cases := []struct {
		name   string
		answer func(mpv net.Conn)
		want   string
	}{
		{
			name:   "mpv stays silent",
			answer: func(net.Conn) {},
			want:   "mpv did not answer in 5s",
		},
		{
			name:   "mpv closes its socket",
			answer: func(mpv net.Conn) { mpv.Close() },
			want:   "mpv closed its socket before it answered",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				log := &logBuffer{}
				mpv, lines := subtitleMPVLogging(t, log, english(subtitlesOn, ""))
				waitForLine(t, lines)
				one.answer(mpv)
				time.Sleep(mpvQueryTimeout)
				synctest.Wait()

				mustMatch(t, log.lines()[1],
					"command: the sidecar connected to mpv's socket, but the subtitle track stays mpv's own choice, "+
						"because the read of playlist-pos failed: "+one.want)
			})
		})
	}
}

// The operator writes the preferences only for a mode that chooses a track,
// so an unset or malformed value is a sidecar that chooses nothing.
func TestParseSubtitlePreferences(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  *subtitlePreferences
	}{
		{"unset", "", nil},
		{"malformed", "on", nil},
		{"written by the operator", `{"subtitles":"auto","sdh":"avoid"}`, &subtitlePreferences{Subtitles: subtitlesAuto, SDH: sdhAvoid}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, fmt.Sprint(parseSubtitlePreferences(one.value)), fmt.Sprint(one.want))
		})
	}
}
