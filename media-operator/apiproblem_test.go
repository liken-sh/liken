package main

// These tests carry RFC 9457's rules for the API's error bodies: the
// title about:blank takes, the members each type carries and the
// order they are written in, and that a HEAD on an error sends no
// body.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProblemTitleFollowsTheType(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		status int
		title  string
		want   string
	}{
		{
			name:   "about:blank on a 404",
			kind:   "about:blank",
			status: http.StatusNotFound,
			title:  "",
			want:   "Not Found",
		},
		{
			name:   "about:blank on a 405",
			kind:   "about:blank",
			status: http.StatusMethodNotAllowed,
			title:  "",
			want:   "Method Not Allowed",
		},
		{
			name:   "about:blank over a title given",
			kind:   "about:blank",
			status: http.StatusBadRequest,
			title:  "the query is not a Media Fragment",
			want:   "Bad Request",
		},
		{
			name:   "a shared type keeps its title",
			kind:   problemNotPlaying,
			status: http.StatusConflict,
			title:  "no Play is running",
			want:   "no Play is running",
		},
		{
			name:   "a shared type on a 503",
			kind:   problemCaptureBusy,
			status: http.StatusServiceUnavailable,
			title:  "the capture is busy",
			want:   "the capture is busy",
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			document := newProblem(one.kind, one.status, one.title, "", "/v1/media#7")

			mustMatch(t, document.Type, one.kind)
			mustMatch(t, document.Title, one.want)
			mustMatch(t, document.Status, one.status)
		})
	}
}

func TestProblemBodyCarriesTheFieldsOfTheType(t *testing.T) {
	cases := []struct {
		name     string
		document problemDocument
		want     string
	}{
		{
			name: "a 404 with no extension member",
			document: newProblem(
				"about:blank",
				http.StatusNotFound,
				"",
				`players.media.liken.sh "studio" not found`,
				"/v1/media/namespaces/media/players/studio#01J8",
			),
			want: `{"type":"about:blank","title":"Not Found","status":404,` +
				`"detail":"players.media.liken.sh \"studio\" not found",` +
				`"instance":"/v1/media/namespaces/media/players/studio#01J8"}`,
		},
		{
			name: "a 502 naming its upstream",
			document: func() problemDocument {
				document := newProblem(
					problemUpstreamFailed,
					http.StatusBadGateway,
					"the upstream answer is not a problem document",
					"unexpected EOF",
					"/v1/media/namespaces/media/players/studio/media.mp4#01J9",
				)
				document.Upstream = "https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.mp4"
				return document
			}(),
			want: `{"type":"https://liken.sh/problems/upstream-failed",` +
				`"title":"the upstream answer is not a problem document","status":502,` +
				`"detail":"unexpected EOF",` +
				`"instance":"/v1/media/namespaces/media/players/studio/media.mp4#01J9",` +
				`"upstream":"https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.mp4"}`,
		},
		{
			name: "a 406 listing the acceptable forms",
			document: func() problemDocument {
				document := newProblem(
					problemNotAcceptable,
					http.StatusNotAcceptable,
					"nothing offered is acceptable",
					"the Accept field excludes video/mp4",
					"/v1/media/namespaces/media/players/studio/media.mp4#01JA",
				)
				document.Acceptable = []acceptableForm{
					{Type: "video/mp4", Href: "/v1/media/namespaces/media/players/studio/media.mp4"},
					{Type: "video/matroska", Href: "/v1/media/namespaces/media/players/studio/media.mkv"},
				}
				return document
			}(),
			want: `{"type":"https://liken.sh/problems/not-acceptable",` +
				`"title":"nothing offered is acceptable","status":406,` +
				`"detail":"the Accept field excludes video/mp4",` +
				`"instance":"/v1/media/namespaces/media/players/studio/media.mp4#01JA",` +
				`"acceptable":[` +
				`{"type":"video/mp4","href":"/v1/media/namespaces/media/players/studio/media.mp4"},` +
				`{"type":"video/matroska","href":"/v1/media/namespaces/media/players/studio/media.mkv"}]}`,
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			writeProblem(recorder, false, one.document)

			mustMatch(t, recorder.Code, one.document.Status)
			mustMatch(t, recorder.Header().Get("Content-Type"), problemContentType)
			mustMatch(t, strings.TrimSuffix(recorder.Body.String(), "\n"), one.want)
		})
	}
}

func TestProblemOnAHeadSendsNoBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	document := newProblem(problemAway, http.StatusConflict, "the Player is away", "no status.screen", "/v1/media#01JB")

	writeProblem(recorder, true, document)

	mustMatch(t, recorder.Code, http.StatusConflict)
	mustMatch(t, recorder.Header().Get("Content-Type"), problemContentType)
	mustMatch(t, recorder.Body.Len(), 0)
}
