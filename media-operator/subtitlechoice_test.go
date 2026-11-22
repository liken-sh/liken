package main

// In these tests, the test acts as mpv on the other end of a net.Pipe.

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// subtitleMPV starts a commander with prefs and returns mpv's end of the
// pipe and the lines that the commander writes after its observe
// requests.
func subtitleMPV(t *testing.T, prefs subtitlePreferences, presentations ...string) (net.Conn, <-chan string) {
	t.Helper()
	return subtitleMPVLogging(t, &logBuffer{}, prefs, presentations...)
}

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

func replyToQuery(t *testing.T, mpv net.Conn, lines <-chan string, property string, data any) {
	t.Helper()
	var sent mpvCommand
	mustSucceed(t, json.Unmarshal([]byte(waitForLine(t, lines)), &sent))
	mustMatch(t, fmt.Sprint(sent.Command), fmt.Sprint([]any{"get_property", property}))
	writeEvent(t, mpv, fmt.Sprintf(`{"request_id":%d,"error":"success","data":%s}`, sent.RequestID, mustEncode(t, data)))
}

// loadedFile answers the playlist-pos and track-list reads of one
// choice.
func loadedFile(t *testing.T, mpv net.Conn, lines <-chan string, position int, file surveyFile) {
	t.Helper()
	replyToQuery(t, mpv, lines, "playlist-pos", position)
	replyToQuery(t, mpv, lines, "track-list", append([]mpvTrack{audio("eng")}, file.tracks...))
}

func mustSetSid(t *testing.T, mpv net.Conn, lines <-chan string, want any) {
	t.Helper()
	var sent mpvCommand
	mustSucceed(t, json.Unmarshal([]byte(waitForLine(t, lines)), &sent))
	mustMatch(t, fmt.Sprint(sent.Command), fmt.Sprint([]any{"set_property", "sid", want}))
	writeEvent(t, mpv, fmt.Sprintf(`{"request_id":%d,"error":"success"}`, sent.RequestID))
}

// presentationWith returns an item's Presentation with the subtitle
// event counts of file.
func presentationWith(t *testing.T, file surveyFile) string {
	t.Helper()
	return string(mustEncode(t, Presentation{Subtitles: file.counts}))
}

// The first item has no event counts, so the short default track keeps
// the full role and wins because it is the default. The second item has counts,
// and the long track wins.
func TestEachLoadedFileGetsTheSubtitleTrackOfItsOwnCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mpv, lines := subtitleMPV(t, english(subtitlesOn, ""), emptyPresentation, presentationWith(t, survey(t, "hawkeye")))
		// The choice on connect finds no file open.
		replyToQuery(t, mpv, lines, "playlist-pos", -1)
		mustNoLine(t, lines)

		writeEvent(t, mpv, `{"event":"file-loaded"}`)
		loadedFile(t, mpv, lines, 0, survey(t, "hawkeye"))
		mustSetSid(t, mpv, lines, 1)

		writeEvent(t, mpv, `{"event":"file-loaded"}`)
		loadedFile(t, mpv, lines, 1, survey(t, "hawkeye"))
		mustSetSid(t, mpv, lines, 3)
	})
}

func TestAFileOpenWhenTheSidecarConnectsGetsItsSubtitleTrack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := &logBuffer{}
		mpv, lines := subtitleMPVLogging(t, log, english(subtitlesOn, ""), presentationWith(t, survey(t, "pluribus")))

		loadedFile(t, mpv, lines, 0, survey(t, "pluribus"))
		mustSetSid(t, mpv, lines, 2)
		synctest.Wait()

		mustMatchAll(t, log.lines(), []string{
			"command: connected to mpv's socket",
			`command: the sidecar connected to mpv's socket, so the sidecar chose subtitle track 2 (eng, full, 569 events) for item 1, ` +
				`because it ranks first of the full and SDH tracks in en, sent ["set_property","sid",2] to mpv, mpv answered success`,
		})
	})
}

// In auto mode, audio in the first language with no forced track shows
// no subtitles.
func TestAFileWithNoTrackToShowGetsNoSubtitles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mpv, lines := subtitleMPV(t, english(subtitlesAuto, ""), presentationWith(t, survey(t, "horrorStory")))

		loadedFile(t, mpv, lines, 0, survey(t, "horrorStory"))
		mustSetSid(t, mpv, lines, "no")
	})
}

func TestATrackSelectedByHandStaysUntilTheNextFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mpv, lines := subtitleMPV(t, english(subtitlesOn, ""), presentationWith(t, survey(t, "pluribus")))
		loadedFile(t, mpv, lines, 0, survey(t, "pluribus"))
		mustSetSid(t, mpv, lines, 2)

		writeEvent(t, mpv, `{"event":"property-change","id":6,"name":"current-tracks/sub/lang","data":"ger"}`)
		mustNoLine(t, lines)
	})
}

func TestNoLanguageInPreferencesLeavesMpvsChoice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := &logBuffer{}
		mpv, lines := subtitleMPVLogging(t, log, subtitlePreferences{Subtitles: subtitlesOn}, presentationWith(t, survey(t, "pluribus")))

		loadedFile(t, mpv, lines, 0, survey(t, "pluribus"))
		mustNoLine(t, lines)

		mustMatchAll(t, log.lines(), []string{
			"command: connected to mpv's socket",
			"command: the sidecar connected to mpv's socket, but the subtitle track of item 1 stays mpv's own choice, " +
				"because neither subtitleLanguages nor audioLanguages names a language",
		})
	})
}

func TestAFileWithNoSubtitleTrackGetsNoCommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mpv, lines := subtitleMPV(t, english(subtitlesOn, ""))

		loadedFile(t, mpv, lines, 0, surveyFile{})
		mustNoLine(t, lines)
	})
}

func TestARefusedReadLogsMpvsError(t *testing.T) {
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
			"command: the sidecar connected to mpv's socket, but the subtitle track stays mpv's own choice, because reading playlist-pos from mpv returned: mpv answered property unavailable",
		})
	})
}

func TestAReadWithNoAnswerEndsTheChoice(t *testing.T) {
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
						"because reading playlist-pos from mpv returned: "+one.want)
			})
		})
	}
}

// The operator writes the preferences only for a mode that chooses a
// track, so an empty or malformed value means the sidecar chooses
// nothing.
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
