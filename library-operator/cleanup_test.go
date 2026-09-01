package main

// what these tests run: the cleanup role against a catalog it can reach
// and one it cannot, with no pod.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// the sweeper reads its library from the environment, because it holds no
// credential, and logs that one line.
func TestNewSweeperReadsItsLibraryFromTheEnvironment(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(catalogAPIVariable, "")
	var logged bytes.Buffer

	sweep := newSweeper(&logged)

	if sweep.library != "house/movies" {
		t.Errorf("library = %q, want house/movies", sweep.library)
	}
	if sweep.catalog.base != defaultCatalogAPI {
		t.Errorf("catalog = %q, want the loopback agent %s", sweep.catalog.base, defaultCatalogAPI)
	}
	if !strings.Contains(logged.String(), "house/movies") {
		t.Errorf("log = %q, want the library it sweeps", logged.String())
	}
}

// a catalog address in the environment is the one it posts to, which is
// how a test drives the role.
func TestNewSweeperTakesTheCatalogAddressItIsGiven(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(catalogAPIVariable, "http://127.0.0.1:9999")

	sweep := newSweeper(&bytes.Buffer{})

	if sweep.catalog.base != "http://127.0.0.1:9999" {
		t.Errorf("catalog = %q, want the address the environment named", sweep.catalog.base)
	}
}

// a log written from the loop's goroutine and read by the test, with the
// lock that makes that safe.
type syncLog struct {
	mutex sync.Mutex
	text  strings.Builder
}

func (l *syncLog) Write(line []byte) (int, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.text.Write(line)
}

func (l *syncLog) String() string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.text.String()
}

// the loop sweeps at once and on every tick, and only the context ends it;
// nothing it reads does.
func TestTheSweeperRepeatsUntilTheContextEnds(t *testing.T) {
	intervalWas := cleanupInterval
	t.Cleanup(func() { cleanupInterval = intervalWas })
	cleanupInterval = time.Millisecond

	catalog, agent := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	logged := &syncLog{}
	sweep := &sweeper{library: "house/movies", catalog: catalog, log: logged}

	running, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sweep.run(running)
	}()

	// the first sweep takes the rows and the loop runs past it, which a peer
	// that pulls late depends on.
	deadline := time.After(scanTestTimeout)
	for strings.Count(logged.String(), "swept house/movies") < 2 {
		select {
		case <-deadline:
			t.Fatalf("the sweeper logged %q, want it to repeat", logged.String())
		default:
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	<-done

	if got := agent.rowsFor(t, "movies", "house/movies"); got != 0 {
		t.Errorf("the departed library holds %d movie rows, want none", got)
	}
	if got := agent.rowsFor(t, "movies", "house/series"); got == 0 {
		t.Error("the surviving library lost its rows")
	}
}

// a failed sweep logs and keeps running, because an exit would take down
// the agent serving the deletes.
func TestTheSweeperLogsAFailureAndCarriesOn(t *testing.T) {
	unwell := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("the agent is unwell"))
	}))
	t.Cleanup(unwell.Close)
	var logged bytes.Buffer
	sweep := &sweeper{
		library: "house/movies",
		catalog: NewCatalog(unwell.URL, unwell.Client()),
		log:     &logged,
	}

	sweep.sweep(t.Context())

	if !strings.Contains(logged.String(), "could not sweep house/movies") {
		t.Errorf("log = %q, want the failure named", logged.String())
	}
	if !strings.Contains(logged.String(), "the agent is unwell") {
		t.Errorf("log = %q, want the agent's own message", logged.String())
	}
}
