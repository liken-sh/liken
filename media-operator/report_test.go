package main

// These tests cover the rule the desk applies to a report and the
// answers the endpoint gives a playback pod, both proved with no pod
// and no cluster.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// reportWakeTimeout is how long a test waits for a wake before it
// calls the loop unwoken.
const reportWakeTimeout = time.Second

// reportDesk builds a desk on a wake channel one deep, because that
// is the channel the operator's loop hands the real one.
func reportDesk(t *testing.T) (*reports, chan struct{}) {
	t.Helper()
	wake := make(chan struct{}, 1)
	return newReports(wake), wake
}

// runningReport is one observation of a run, as the supervisor sends
// it while the position advances.
func runningReport(token string) playReport {
	return playReport{
		Namespace: "house",
		Name:      "movie",
		Token:     token,
		Item:      1,
		Position:  "0:01:00",
		Duration:  "1:30:00",
	}
}

func reportBody(t *testing.T, report playReport) string {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// postReport sends one report through the endpoint's own routing,
// with no server between the test and the handler.
func postReport(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/report", strings.NewReader(body)))
	return recorder
}

func waitForReportWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(reportWakeTimeout):
		t.Fatal("no wake reached the loop")
	}
}

// expectNoReportWake needs no waiting: the wake is already there or
// it is not, because the handler pokes it before it answers.
func expectNoReportWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
		t.Fatal("a wake reached the loop")
	default:
	}
}

func TestTheEndpointAcceptsAMatchingReportOverHTTP(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.remember("house", "movie", "minted")
	server := httptest.NewServer(reportHandler(desk))
	t.Cleanup(server.Close)

	response, err := http.Post(server.URL+"/report", "application/json",
		strings.NewReader(reportBody(t, runningReport("minted"))))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	answered, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", response.StatusCode)
	}
	// The pod reads nothing back, so the endpoint says nothing.
	if len(answered) != 0 {
		t.Errorf("body = %q, want an empty body", answered)
	}
	stored := desk.latestFor("house", "movie")
	if stored == nil {
		t.Fatal("the desk holds no report for house/movie")
	}
	if stored.Item != 1 || stored.Position != "0:01:00" || stored.Duration != "1:30:00" {
		t.Errorf("stored report = %+v", *stored)
	}
	waitForReportWake(t, wake)
}

func TestAReportWithTheWrongTokenIsRefusedAndChangesNothing(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.remember("house", "movie", "minted")
	handler := reportHandler(desk)
	if accepted := postReport(t, handler, reportBody(t, runningReport("minted"))); accepted.Code != http.StatusOK {
		t.Fatalf("the matching report answered %d, want 200", accepted.Code)
	}
	waitForReportWake(t, wake)

	guessed := runningReport("guessed")
	guessed.Item = 2
	guessed.Position = "0:02:00"
	refused := postReport(t, handler, reportBody(t, guessed))

	if refused.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", refused.Code)
	}
	stored := desk.latestFor("house", "movie")
	if stored == nil {
		t.Fatal("the accepted report is gone")
	}
	if stored.Item != 1 || stored.Position != "0:01:00" {
		t.Errorf("stored report = %+v, want the accepted one", *stored)
	}
	expectNoReportWake(t, wake)
}

// Every one of these reports names a run the desk cannot attach it
// to, and the endpoint answers all of them the same way, so a probe
// learns nothing about which plays exist.
func TestAReportTheDeskCannotAttachToARunIsRefused(t *testing.T) {
	cases := []struct {
		name   string
		report playReport
	}{
		{
			name:   "a play this operator does not run",
			report: playReport{Namespace: "attic", Name: "radio", Token: "minted"},
		},
		{
			name:   "an empty token",
			report: playReport{Namespace: "house", Name: "movie"},
		},
		{
			name:   "a token the desk did not mint",
			report: playReport{Namespace: "house", Name: "movie", Token: "guessed"},
		},
		{
			name:   "no namespace",
			report: playReport{Name: "movie", Token: "minted"},
		},
		{
			name:   "no name",
			report: playReport{Namespace: "house", Token: "minted"},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			desk, wake := reportDesk(t)
			desk.remember("house", "movie", "minted")

			refused := postReport(t, reportHandler(desk), reportBody(t, each.report))

			if refused.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", refused.Code)
			}
			if stored := desk.latestFor(each.report.Namespace, each.report.Name); stored != nil {
				t.Errorf("stored report = %+v, want none", *stored)
			}
			expectNoReportWake(t, wake)
		})
	}
}

func TestTheNewestReportIsTheOneTheDeskHolds(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.remember("house", "movie", "minted")
	handler := reportHandler(desk)

	if first := postReport(t, handler, reportBody(t, runningReport("minted"))); first.Code != http.StatusOK {
		t.Fatalf("the first report answered %d, want 200", first.Code)
	}
	later := runningReport("minted")
	later.Item = 2
	later.Paused = true
	later.Position = "0:45:00"
	if second := postReport(t, handler, reportBody(t, later)); second.Code != http.StatusOK {
		t.Fatalf("the second report answered %d, want 200", second.Code)
	}

	stored := desk.latestFor("house", "movie")
	if stored == nil {
		t.Fatal("the desk holds no report for house/movie")
	}
	if stored.Item != 2 || stored.Position != "0:45:00" || !stored.Paused {
		t.Errorf("stored report = %+v, want the second one", *stored)
	}
	// Two reports leave one wake, because the pass that answers it
	// reads the whole collection.
	waitForReportWake(t, wake)
	expectNoReportWake(t, wake)
}

func TestABodyThatDoesNotDecodeIsRefused(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.remember("house", "movie", "minted")

	refused := postReport(t, reportHandler(desk), `{"namespace":"house","name":`)

	if refused.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", refused.Code)
	}
	if stored := desk.latestFor("house", "movie"); stored != nil {
		t.Errorf("stored report = %+v, want none", *stored)
	}
	expectNoReportWake(t, wake)
}

// The mux answers a wrong method and a wrong path, so the handler
// reads only the requests a supervisor sends.
func TestTheEndpointAnswersAWrongMethodAndAWrongPath(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{name: "a GET of the report path", method: http.MethodGet, path: "/report", status: http.StatusMethodNotAllowed},
		{name: "a POST to another path", method: http.MethodPost, path: "/reports", status: http.StatusNotFound},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			desk, wake := reportDesk(t)
			desk.remember("house", "movie", "minted")
			recorder := httptest.NewRecorder()

			reportHandler(desk).ServeHTTP(recorder, httptest.NewRequest(each.method, each.path, nil))

			if recorder.Code != each.status {
				t.Errorf("status = %d, want %d", recorder.Code, each.status)
			}
			expectNoReportWake(t, wake)
		})
	}
}

func TestTheDeskForgetsARunThatIsGoneAndKeepsTheOneThatIsNot(t *testing.T) {
	desk, _ := reportDesk(t)
	handler := reportHandler(desk)
	desk.remember("house", "movie", "minted")
	desk.remember("attic", "radio", "other")
	living := runningReport("minted")
	gone := playReport{Namespace: "attic", Name: "radio", Token: "other", Item: 1, Position: "0:02:00"}
	if answer := postReport(t, handler, reportBody(t, living)); answer.Code != http.StatusOK {
		t.Fatalf("house/movie answered %d, want 200", answer.Code)
	}
	if answer := postReport(t, handler, reportBody(t, gone)); answer.Code != http.StatusOK {
		t.Fatalf("attic/radio answered %d, want 200", answer.Code)
	}

	desk.retain(map[string]bool{runKey("house", "movie"): true})

	if got := desk.token("house", "movie"); got != "minted" {
		t.Errorf("the live run's token = %q, want minted", got)
	}
	if got := desk.token("attic", "radio"); got != "" {
		t.Errorf("the gone run's token = %q, want none", got)
	}
	if desk.latestFor("house", "movie") == nil {
		t.Error("the live run's report is gone")
	}
	if stored := desk.latestFor("attic", "radio"); stored != nil {
		t.Errorf("the gone run's report = %+v, want none", *stored)
	}
}
