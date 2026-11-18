package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// actionsAPI starts a fake Actions API that responds with status and
// body to the query for successful push runs of ci.yaml on main, and
// with 404 to any other path, query, or token.
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

func TestTheNewestSuccessfulRunIsReadFromTheActionsAPI(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       string
		fails      bool
	}{
		{"a run that passed", `{"workflow_runs":[{"head_sha":"abc123"}]}`, http.StatusOK, "abc123", false},
		{"no run that passed", `{"workflow_runs":[]}`, http.StatusOK, "", false},
		{"a refusal", `{}`, http.StatusForbidden, "", true},
		{"a response that is not JSON", `<html>`, http.StatusOK, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runs := Runs{API: actionsAPI(t, c.status, c.body), Repository: "liken-sh/liken", Token: "token"}
			got, err := runs.NewestSuccessful("ci.yaml")
			if got != c.want || (err != nil) != c.fails {
				t.Errorf("NewestSuccessful = %q, %v, want %q and failure %v", got, err, c.want, c.fails)
			}
		})
	}
}
