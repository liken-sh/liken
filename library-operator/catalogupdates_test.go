package main

// What these tests prove: the per-table update stream the reporter
// follows, against a stream of the shapes the agent sends and against
// the fake agent's own endpoint.

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// One open update stream of the fake agent: the table it
// follows and the channel a write on that table wakes.
type tableWatcher struct {
	table string
	woken chan struct{}
}

// Answers the update stream of one table the way the agent
// does: the headers first, then one event per write that names the
// table, until the caller goes away. The events carry the primary key
// of the row and never its library.
func (a *sqliteAgent) serveUpdates(w http.ResponseWriter, r *http.Request, table string) {
	watcher := tableWatcher{table: table, woken: make(chan struct{}, 1)}
	a.mutex.Lock()
	a.watchers = append(a.watchers, watcher)
	a.mutex.Unlock()

	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-watcher.woken:
			_ = enc.Encode(map[string]any{"notify": []any{"update", []any{table}}})
			flusher.Flush()
		}
	}
}

// Wakes every stream whose table a posted statement names, the
// way an applied write reaches the agent's update streams.
func (a *sqliteAgent) notifyWatchers(statements []capturedStatement) {
	a.mutex.Lock()
	watchers := make([]tableWatcher, len(a.watchers))
	copy(watchers, a.watchers)
	a.mutex.Unlock()

	for _, watcher := range watchers {
		named := false
		for _, statement := range statements {
			if strings.Contains(statement.sql, " "+watcher.table+" ") {
				named = true
			}
		}
		if !named {
			continue
		}
		select {
		case watcher.woken <- struct{}{}:
		default:
		}
	}
}

// Every event of the stream is one change, an error event ends the
// stream, and a refused stream is an error the caller answers with a
// fresh one.
func TestFollowUpdatesReadsEveryEventAndSurfacesTheRest(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		changes int
		opened  bool
		wantErr bool
	}{
		{
			name: "an update and a delete", status: http.StatusOK, opened: true, changes: 2,
			body: `{"notify":["update",["house/movies","movie:tmdb:1"]]}` + "\n" +
				`{"notify":["delete",["house/movies","movie:tmdb:2"]]}` + "\n",
		},
		{
			name: "an empty line between events", status: http.StatusOK, opened: true, changes: 1,
			body: "\n" + `{"notify":["update",["house/movies"]]}` + "\n",
		},
		{
			name: "an event of no kind the reader acts on", status: http.StatusOK, opened: true,
			body: `{"eoq":{"time":0.1}}` + "\n",
		},
		{
			name: "an error event", status: http.StatusOK, opened: true, wantErr: true,
			body: `{"error":"no such table: movies"}` + "\n",
		},
		{
			name: "a line that is not JSON", status: http.StatusOK, opened: true, wantErr: true,
			body: "not json\n",
		},
		{
			name: "a refused stream", status: http.StatusInternalServerError, wantErr: true,
			body: "the agent is unwell",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			catalog := subscriptionServer(t, testCase.status, testCase.body)
			changes, opened := 0, false

			err := catalog.followUpdates(t.Context(), "movies",
				func() { opened = true }, func() { changes++ })

			if (err != nil) != testCase.wantErr {
				t.Errorf("error = %v, want an error: %v", err, testCase.wantErr)
			}
			if changes != testCase.changes {
				t.Errorf("changes = %d, want %d", changes, testCase.changes)
			}
			if opened != testCase.opened {
				t.Errorf("opened = %v, want %v", opened, testCase.opened)
			}
		})
	}
}

// A stream against an agent that answers no request is an error, so
// the reporter opens it again after its backoff.
func TestFollowUpdatesFailsOnAnAgentItCannotReach(t *testing.T) {
	catalog := NewCatalog("http://127.0.0.1:1", http.DefaultClient)

	if err := catalog.followUpdates(t.Context(), "movies", func() {}, func() {}); err == nil {
		t.Error("the stream returned no error, want the failed request")
	}
}

// A row written to a table reaches that table's stream, which is the
// signal the reporter republishes on, and no other table's stream.
func TestAWriteReachesItsOwnTablesUpdateStream(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	movies := make(chan struct{}, 4)
	series := make(chan struct{}, 4)
	var streams sync.WaitGroup
	for table, changes := range map[string]chan struct{}{"movies": movies, "series": series} {
		streams.Add(1)
		opened := make(chan struct{})
		go func() {
			defer streams.Done()
			_ = catalog.followUpdates(t.Context(), table,
				sync.OnceFunc(func() { close(opened) }),
				func() { changes <- struct{}{} })
		}()
		<-opened
	}
	t.Cleanup(streams.Wait)

	if err := upsertWalk(t.Context(), catalog,
		walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-movies:
	case <-time.After(scanTestTimeout):
		t.Fatal("the write never reached the movies stream")
	}
	select {
	case <-series:
		t.Error("the write reached the stream of a table it did not name")
	case <-time.After(50 * time.Millisecond):
	}
}
