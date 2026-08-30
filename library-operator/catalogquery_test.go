package main

// These tests prove the query client against a small HTTP server that
// streams the newline-delimited events a Corrosion agent sends, so the row
// decoding, the count decoding, and the error path are proved with no agent.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// streamingServer answers every query with a fixed body and status, so a
// test drives the stream the client reads.
func streamingServer(t *testing.T, status int, body string) *Catalog {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client())
}

func TestQueryStringsReadsEveryRow(t *testing.T) {
	body := `{"columns":["id"]}` + "\n" +
		`{"row":[1,["movie:tmdb:1"]]}` + "\n" +
		`{"row":[2,["movie:tmdb:2"]]}` + "\n" +
		`{"eoq":{"time":0.1}}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	ids, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "movie:tmdb:1" || ids[1] != "movie:tmdb:2" {
		t.Errorf("ids = %v, want the two rows", ids)
	}
}

func TestQueryIntReadsACount(t *testing.T) {
	body := `{"columns":["count(*)"]}` + "\n" +
		`{"row":[1,[42]]}` + "\n" +
		`{"eoq":{"time":0.0}}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	count, err := catalog.queryInt(context.Background(), "SELECT count(*) FROM movies", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 42 {
		t.Errorf("count = %d, want 42", count)
	}
}

func TestQueryReadsNoRowAsZero(t *testing.T) {
	body := `{"columns":["count(*)"]}` + "\n" + `{"eoq":{"time":0.0}}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	count, err := catalog.queryInt(context.Background(), "SELECT count(*) FROM movies", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 for an empty result", count)
	}
}

func TestQuerySurfacesAnErrorEvent(t *testing.T) {
	body := `{"columns":["id"]}` + "\n" + `{"error":"no such table: movies"}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	_, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies", nil)
	if err == nil {
		t.Error("the client hid a streamed error event")
	}
}

func TestQueryRejectsAMalformedLine(t *testing.T) {
	catalog := streamingServer(t, http.StatusOK, "this is not json\n")

	_, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies", nil)
	if err == nil {
		t.Error("the client read a malformed line as a row")
	}
}

func TestQueryStringsSendsParameters(t *testing.T) {
	body := `{"columns":["id"]}` + "\n" + `{"row":[1,["one"]]}` + "\n" + `{"eoq":{"time":0.0}}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	ids, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies WHERE library = ?", []any{"house/movies"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "one" {
		t.Errorf("ids = %v, want the one row", ids)
	}
}

func TestQuerySurfacesANon2xxStatus(t *testing.T) {
	catalog := streamingServer(t, http.StatusInternalServerError, "the agent is unwell")

	_, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies", nil)
	if err == nil {
		t.Error("the client hid a non-2xx status")
	}
}
