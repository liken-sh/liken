package worker

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func serve(s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(body)))
	return w
}

func extractRequest(t *testing.T, request ExtractRequest) []byte {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// Each extraction logs one line with the frame, the stars, the time,
// and the radius, so the pod's log shows each attempt of a centering.
func TestAnExtractionThroughTheServerAnswersAndLogs(t *testing.T) {
	p := fakePrograms(t, programsWork)
	p.put(t, centeringFrame, simulatorFrame)
	var logs bytes.Buffer
	reply := serve(NewServer(p.extractor, &logs), http.MethodPost, PathExtract,
		extractRequest(t, ExtractRequest{Frame: centeringFrame, Hint: nearTheField}))
	var answer ExtractAnswer
	if err := json.Unmarshal(reply.Body.Bytes(), &answer); err != nil || answer.Stars != 1675 {
		t.Errorf("answered %d %.200s", reply.Code, reply.Body)
	}
	if !strings.Contains(logs.String(), "extracted 1675 stars of median FWHM 4.93 pixels from "+centeringFrame) ||
		!strings.Contains(logs.String(), "with a radius of 5 degrees") {
		t.Errorf("logged %q", logs.String())
	}
}

// The outbox answer lists each target's frames, and leaves out the
// centering frames and the files of no frame.
func TestTheOutboxAnswerListsTheTargetsFrames(t *testing.T) {
	p := fakePrograms(t, programsWork)
	for _, path := range []string{
		"east-main/east-tonight/m42/M42_0002.fits",
		"east-main/east-tonight/m42/Red/M42_0001.fits",
		"east-main/east-tonight/_centering/0f3a.fits",
		"east-main/stray.fits",
		"east-main/east-tonight/m42/notes.txt",
	} {
		p.put(t, path, nil)
	}
	reply := serve(NewServer(p.extractor, io.Discard), http.MethodGet, PathOutbox, nil)
	var answer OutboxAnswer
	if err := json.Unmarshal(reply.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	want := []string{"east-main/east-tonight/m42/M42_0002.fits", "east-main/east-tonight/m42/Red/M42_0001.fits"}
	if !slices.Equal(answer.Frames, want) || answer.FreeBytes <= 0 {
		t.Errorf("answered %+v, want %q and the free bytes", answer, want)
	}
}

// An empty outbox answers an empty list, not null.
func TestAnEmptyOutboxAnswersNoFrames(t *testing.T) {
	p := fakePrograms(t, programsWork)
	reply := serve(NewServer(p.extractor, io.Discard), http.MethodGet, PathOutbox, nil)
	if !strings.Contains(reply.Body.String(), `"frames":[]`) {
		t.Errorf("answered %s", reply.Body)
	}
}

// The worker deletes a centering frame after its solve, and nothing
// else: a FRAMEID that names a path is refused.
func TestADeleteRemovesOnlyThatCenteringFrame(t *testing.T) {
	p := fakePrograms(t, programsWork)
	p.put(t, centeringFrame, simulatorFrame)
	p.put(t, "east-main/east-tonight/_centering/1b2c.fits", simulatorFrame)
	s := NewServer(p.extractor, io.Discard)
	cases := []struct {
		path   string
		status int
	}{
		{PathCentering + "0f3a", http.StatusNoContent},
		{PathCentering + "0f3a", http.StatusNotFound},
		{PathCentering + "..", http.StatusBadRequest},
		{PathCentering + "*", http.StatusBadRequest},
		{PathCentering, http.StatusBadRequest},
	}
	for _, c := range cases {
		if reply := serve(s, http.MethodDelete, c.path, nil); reply.Code != c.status {
			t.Errorf("DELETE %s answered %d, want %d", c.path, reply.Code, c.status)
		}
	}
	if _, err := os.Stat(filepath.Join(p.extractor.Outbox, "east-main/east-tonight/_centering/1b2c.fits")); err != nil {
		t.Errorf("the other centering frame: %v", err)
	}
}

func TestEachFailedRequestAnswersAStatusAndAReason(t *testing.T) {
	p := fakePrograms(t, programsFail)
	p.put(t, centeringFrame, simulatorFrame)
	gone := p.extractor
	gone.Outbox = filepath.Join(t.TempDir(), "gone")
	cases := []struct {
		name   string
		e      Extractor
		method string
		path   string
		body   []byte
		status int
	}{
		{"a GET of the extraction", p.extractor, http.MethodGet, PathExtract, nil, http.StatusMethodNotAllowed},
		{"a POST of the outbox", p.extractor, http.MethodPost, PathOutbox, nil, http.StatusMethodNotAllowed},
		{"another path", p.extractor, http.MethodGet, "/", nil, http.StatusNotFound},
		{"a body that is not JSON", p.extractor, http.MethodPost, PathExtract, []byte("{"), http.StatusBadRequest},
		{"a frame that is missing", p.extractor, http.MethodPost, PathExtract,
			extractRequest(t, ExtractRequest{Frame: "east-main/east-tonight/_centering/none.fits", Hint: nearTheField}), http.StatusNotFound},
		{"a program that fails", p.extractor, http.MethodPost, PathExtract,
			extractRequest(t, ExtractRequest{Frame: centeringFrame, Hint: nearTheField}), http.StatusInternalServerError},
		{"an outbox that is not mounted", gone, http.MethodGet, PathOutbox, nil, http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reply := serve(NewServer(c.e, io.Discard), c.method, c.path, c.body)
			if reply.Code != c.status || strings.TrimSpace(reply.Body.String()) == "" {
				t.Errorf("answered %d %q, want %d with a reason", reply.Code, reply.Body, c.status)
			}
		})
	}
}
