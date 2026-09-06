package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// franchiseScanner is one franchises scan Job over a checkout, wired to a
// real SQLite catalog on the shipped schema. The rows and the prune run with
// no cluster and no agent.
func franchiseScanner(t *testing.T, checkout, art string) (*scanner, *sqliteAgent) {
	t.Helper()
	catalog, agent := newSQLiteCatalog(t)
	return franchiseScannerOn(t, catalog, checkout, art), agent
}

// franchiseScannerOn is a second Job over the same catalog, which is what a
// scan on a schedule is: a new pod on the same two claims, and the rows the
// last scan left.
func franchiseScannerOn(t *testing.T, catalog *Catalog, checkout, art string) *scanner {
	t.Helper()
	return &scanner{
		root:    checkout,
		art:     art,
		library: "house/franchises",
		kind:    libraryKindFranchises,
		catalog: catalog,
		log:     io.Discard,
		report:  libraryReport{LastWalk: time.Now().UTC(), LastChange: time.Now().UTC()},
	}
}

// A franchises scan reads every franchise.yaml on the storage claim and
// writes the three tables.
func TestTheFranchiseScanWritesTheCheckoutIntoTheCatalog(t *testing.T) {
	checkout := franchiseCheckout(t, map[string]string{
		"Star Wars/franchise.yaml": wholeFranchiseFile,
		"Firefly/franchise.yaml":   "name: Firefly\norder:\n  - series: tvdb:78874\n",
	})
	scan, agent := franchiseScanner(t, checkout, t.TempDir())

	if err := scan.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	for table, want := range map[string]int{"franchises": 2, "franchise_members": 4, "franchise_runs": 5} {
		if held := agent.rowsFor(t, table, "house/franchises"); held != want {
			t.Errorf("%s holds %d rows, want %d", table, held, want)
		}
	}
	if scan.report.Titles != 2 {
		t.Errorf("titles = %d, want the two directories the checkout holds", scan.report.Titles)
	}
}

// The art is read off the art claim and never off the checkout, so the row
// names the file the fetch wrote on the claim the screen mounts.
func TestTheFranchiseScanReadsTheArtOffTheArtClaim(t *testing.T) {
	checkout := franchiseCheckout(t, map[string]string{
		"Star Wars/franchise.yaml": wholeFranchiseFile,
		"Star Wars/banner.jpg":     "not the art the row names",
	})
	art := t.TempDir()
	if err := os.MkdirAll(filepath.Join(art, "Star Wars"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(art, "Star Wars", "poster.jpg"), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan, agent := franchiseScanner(t, checkout, art)

	if err := scan.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	_, rows, err := agent.readAll(`SELECT art, arts FROM franchises`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0][0] != "Star Wars/poster.jpg" {
		t.Errorf("art = %v, want the poster on the art claim", rows)
	}
	if len(rows) == 1 && strings.Contains(rows[0][1].(string), "banner") {
		t.Errorf("arts = %v, want no file off the checkout", rows[0][1])
	}
}

// Every scan walks. The checkout is a mounted claim and the files are a few
// hundred kilobytes, so a scan on a schedule reads them again the way every
// other kind's scan does.
func TestEveryFranchiseScanWalksTheCheckout(t *testing.T) {
	checkout := franchiseCheckout(t, map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile})
	art := t.TempDir()
	scan, agent := franchiseScanner(t, checkout, art)
	if err := scan.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	agent.largestBatch = 0
	second := franchiseScannerOn(t, scan.catalog, checkout, art)
	if err := second.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	if agent.largestBatch == 0 {
		t.Error("the second scan posted no statement, want it walked the checkout again")
	}
	if held := agent.rowsFor(t, "franchises", "house/franchises"); held != 1 {
		t.Errorf("franchises holds %d rows, want the one franchise the checkout holds", held)
	}
	if second.report.Titles != 1 {
		t.Errorf("titles = %d, want the one directory the checkout holds", second.report.Titles)
	}
}

// A catalog that answers one kind of request and refuses another, so a test
// drives the step of a scan that fails. Transactions is how many transactions
// it answers before it refuses, and queries says whether it answers a read at
// all.
func refusingCatalog(t *testing.T, transactions int, queries bool) *Catalog {
	t.Helper()
	answered := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, queriesPath) {
			if !queries {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, "{\"columns\":[\"n\"]}\n{\"row\":[1,[0]]}\n{\"eoq\":{\"time\":0}}\n")
			return
		}
		answered++
		if answered > transactions {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var posted []any
		_ = json.NewDecoder(r.Body).Decode(&posted)
		results := make([]map[string]any, len(posted))
		for i := range results {
			results[i] = map[string]any{"rows_affected": 1}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client())
}

// A scan whose catalog refuses a step fails the Job, so Kubernetes runs it
// again. It never prunes on a catalog it could not read, because a prune with
// no marks would sweep the whole library.
func TestTheFranchiseScanFailsOnACatalogItCannotWrite(t *testing.T) {
	checkout := franchiseCheckout(t, map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile})
	cases := []struct {
		name         string
		transactions int
		queries      bool
		says         string
	}{
		{"the seen table", 0, true, "ensure the seen table"},
		{"the count before the walk", 1, false, "count the catalog"},
		{"the rows the walk read", 1, true, "write the franchises"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			scan := franchiseScannerOn(t, refusingCatalog(t, testCase.transactions, testCase.queries),
				checkout, t.TempDir())

			err := scan.walkOnce(t.Context())

			if err == nil {
				t.Fatalf("the scan finished, want it failed on %s", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.says) {
				t.Errorf("the error is %q, want it to name %q", err, testCase.says)
			}
		})
	}
}

// A checkout the walk could not read in full prunes nothing and fails the
// Job, so a claim that answered half its files never sweeps a franchise off
// the wall.
func TestTheFranchiseScanPrunesNothingOnACheckoutItCouldNotRead(t *testing.T) {
	scan, agent := franchiseScanner(t, franchiseCheckout(t,
		map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile}), t.TempDir())
	if err := scan.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	// A second checkout that holds neither file the first held, and whose
	// one entry is a directory where a franchise.yaml goes, which every read
	// of it refuses.
	broken := franchiseCheckout(t, map[string]string{"Alien/franchise.yaml/keep": "x"})
	second := franchiseScannerOn(t, scan.catalog, broken, t.TempDir())
	err := second.walkOnce(t.Context())

	if !errors.Is(err, errIncompleteWalk) {
		t.Fatalf("the scan ended with %v, want the incomplete walk", err)
	}
	if held := agent.rowsFor(t, "franchises", "house/franchises"); held != 1 {
		t.Errorf("franchises holds %d rows, want the one the last good scan left", held)
	}
}

// franchiseScanJob is one franchises scan Job on the test broker and the
// recording catalog. The run rows it writes are read back as statements.
func franchiseScanJob(t *testing.T, checkout string) (*scanner, *catalogRecorder, <-chan *fakeBroker) {
	t.Helper()
	address, accepted := testBroker(t)
	shorterBackoff(t)
	catalog, recorder := recordingCatalog(t)
	scan := &scanner{
		statusTopic: libraryStatusTopic(defaultTopicBase, "house", "franchises"),
		root:        checkout,
		art:         t.TempDir(),
		library:     "house/franchises",
		kind:        libraryKindFranchises,
		catalog:     catalog,
		log:         io.Discard,
		job:         "franchises-scan-1",
		echoTimeout: scanTestTimeout,
	}
	scan.echo = newEchoWaiter(scan.statusTopic, scan.worker(), scan.job)
	scan.bus = newBus(address, "scan-house-franchises", nil, nil, scan.echo.note)
	return scan, recorder, accepted
}

// lastRunPosted is the parameters of the last runs row a Job posted.
func lastRunPosted(t *testing.T, recorder *catalogRecorder) []any {
	t.Helper()
	posted := runsPosted(recorder)
	if len(posted) == 0 {
		t.Fatal("the job posted no run")
	}
	return posted[len(posted)-1].params
}

// A Job that walked its checkout writes a finished run with no failure, the
// run the reporter echoes back.
func TestTheFranchiseScanJobWritesItsRun(t *testing.T) {
	checkout := franchiseCheckout(t, map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile})
	scan, recorder, accepted := franchiseScanJob(t, checkout)
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	echoTheRun(t, accepted, scan.echo)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	params := lastRunPosted(t, recorder)
	if params[7] != "" {
		t.Errorf("the run carries the failure %v, want none", params[7])
	}
}

// A Job whose checkout is not there writes a run that names the failure,
// which the operator reads as the Failed phase.
func TestTheFranchiseScanJobReportsACheckoutItCouldNotRead(t *testing.T) {
	scan, recorder, accepted := franchiseScanJob(t, filepath.Join(t.TempDir(), "no-such-checkout"))
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	echoTheRun(t, accepted, scan.echo)
	if err := <-done; err == nil {
		t.Fatal("the job succeeded, want it failed on a checkout it could not read")
	}

	params := lastRunPosted(t, recorder)
	failure, _ := params[7].(string)
	if failure == "" {
		t.Error("the run carries no failure, want the walk it could not finish")
	}
}
