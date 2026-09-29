package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// actionsAPI serves the runs of ci.yaml, and checks the query that
// asks for the newest green push to main.
func actionsAPI(t *testing.T, status int, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/repos/liken-sh/liken/actions/workflows/ci.yaml/runs" ||
			q.Get("branch") != "main" || q.Get("status") != "success" || q.Get("event") != "push" ||
			r.Header.Get("Authorization") != "Bearer token" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestTheNewestGreenRunIsReadFromTheActionsAPI(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       string
		fails      bool
	}{
		{"a green run", `{"workflow_runs":[{"head_sha":"abc123"}]}`, http.StatusOK, "abc123", false},
		{"no green run", `{"workflow_runs":[]}`, http.StatusOK, "", false},
		{"a refusal", `{}`, http.StatusForbidden, "", true},
		{"an answer that is not JSON", `<html>`, http.StatusOK, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runs := Runs{API: actionsAPI(t, c.status, c.body), Repository: "liken-sh/liken", Token: "token"}
			got, err := runs.NewestGreen("ci.yaml")
			if got != c.want || (err != nil) != c.fails {
				t.Errorf("NewestGreen = %q, %v", got, err)
			}
		})
	}
}
