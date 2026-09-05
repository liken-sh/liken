package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// franchiseScanner is one franchises scan Job over a repository, wired to a
// real SQLite catalog on the shipped schema. The clone, the rows, and the
// prune all run with no cluster and no agent.
func franchiseScanner(t *testing.T, url, ref string) (*scanner, *sqliteAgent) {
	t.Helper()
	catalog, agent := newSQLiteCatalog(t)
	return franchiseScannerOn(t, catalog, url, ref), agent
}

// franchiseScannerOn is a second Job over the same catalog, which is what a
// scan on a schedule is: a new pod, a new checkout, and the rows the last scan
// left.
func franchiseScannerOn(t *testing.T, catalog *Catalog, url, ref string) *scanner {
	t.Helper()
	return &scanner{
		root:     t.TempDir(),
		checkout: filepath.Join(t.TempDir(), "checkout"),
		library:  "house/franchises",
		kind:     libraryKindFranchises,
		git:      LibraryGit{URL: url, Ref: ref},
		catalog:  catalog,
		log:      io.Discard,
		report:   libraryReport{LastWalk: time.Now().UTC(), LastChange: time.Now().UTC()},
	}
}

// A franchises scan clones the ref, reads every franchise.yaml, and writes the
// three tables. It holds the commit it read, which the Job's run row then
// carries.
func TestTheFranchiseScanWritesTheRepositoryIntoTheCatalog(t *testing.T) {
	url := gitRepository(t, "main", map[string]string{
		"Star Wars/franchise.yaml": wholeFranchiseFile,
		"Firefly/franchise.yaml":   "name: Firefly\norder:\n  - series: tvdb:78874\n",
	})
	scan, agent := franchiseScanner(t, url, "main")

	if err := scan.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	for table, want := range map[string]int{"franchises": 2, "franchise_members": 4, "franchise_runs": 5} {
		if held := agent.rowsFor(t, table, "house/franchises"); held != want {
			t.Errorf("%s holds %d rows, want %d", table, held, want)
		}
	}
	if scan.lastCommit() != headOf(t, url, "main") {
		t.Errorf("commit = %q, want the commit the clone read", scan.lastCommit())
	}
	if scan.report.Titles != 2 {
		t.Errorf("titles = %d, want the two directories the repository holds", scan.report.Titles)
	}
}

// A scan that clones the commit the last scan read writes no row. The files
// change only when the commit changes, so a scan on a schedule costs one clone
// and nothing else.
func TestTheFranchiseScanWritesNothingOnTheCommitItAlreadyRead(t *testing.T) {
	url := gitRepository(t, "main", map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile})
	scan, agent := franchiseScanner(t, url, "main")
	if err := scan.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	second := franchiseScannerOn(t, scan.catalog, url, "main")
	second.commit = scan.lastCommit()
	before := agent.largestBatch
	agent.largestBatch = 0
	if err := second.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	if agent.largestBatch != 0 {
		t.Errorf("the second scan posted a batch of %d statements, want none", agent.largestBatch)
	}
	if before == 0 {
		t.Error("the first scan posted no statement, so the second proves nothing")
	}
	if held := agent.rowsFor(t, "franchises", "house/franchises"); held != 1 {
		t.Errorf("franchises holds %d rows, want the one the first scan wrote", held)
	}
}

// A clone that fails leaves every row as it was, because a forge outage must
// not empty every franchise page for a day. The scan never runs mark-and-sweep
// on a checkout it does not have.
func TestTheFranchiseScanKeepsTheTablesWhenTheCloneFails(t *testing.T) {
	url := gitRepository(t, "main", map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile})
	scan, agent := franchiseScanner(t, url, "main")
	if err := scan.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	failing := franchiseScannerOn(t, scan.catalog,
		"https://forge.invalid/guid.foo/fiction-franchises.git", "main")
	failing.commit = scan.lastCommit()
	err := failing.walkOnce(t.Context())

	if err == nil {
		t.Fatal("the scan finished, want it failed on an unreachable forge")
	}
	for table, want := range map[string]int{"franchises": 1, "franchise_members": 3, "franchise_runs": 5} {
		if held := agent.rowsFor(t, table, "house/franchises"); held != want {
			t.Errorf("%s holds %d rows, want the %d the last good scan left", table, held, want)
		}
	}
	if failing.lastCommit() != scan.lastCommit() {
		t.Errorf("commit = %q, want the commit the last good scan read", failing.lastCommit())
	}
}

// franchiseScanJob is one franchises scan Job on the test broker and the
// recording catalog. The run rows it writes are read back as statements.
func franchiseScanJob(t *testing.T, url, ref string) (*scanner, *catalogRecorder, <-chan *fakeBroker) {
	t.Helper()
	address, accepted := testBroker(t)
	shorterBackoff(t)
	catalog, recorder := recordingCatalog(t)
	scan := &scanner{
		statusTopic: libraryStatusTopic(defaultTopicBase, "house", "franchises"),
		root:        t.TempDir(),
		checkout:    filepath.Join(t.TempDir(), "checkout"),
		library:     "house/franchises",
		kind:        libraryKindFranchises,
		git:         LibraryGit{URL: url, Ref: ref},
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

// The Job's finished run carries the commit the scan read, which is where
// status.commit comes from, and a run that finished its work carries no
// failure.
func TestTheFranchiseScanJobReportsTheCommit(t *testing.T) {
	url := gitRepository(t, "main", map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile})
	scan, recorder, accepted := franchiseScanJob(t, url, "main")
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	echoTheRun(t, accepted, scan.echo)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	params := lastRunPosted(t, recorder)
	if params[7] != headOf(t, url, "main") {
		t.Errorf("the run carries the commit %v, want the one the clone read", params[7])
	}
	if params[8] != "" {
		t.Errorf("the run carries the failure %v, want none", params[8])
	}
}

// A Job whose clone failed writes a run that names the failure, which the
// operator reads as the Failed phase. It keeps the commit the last good scan
// read, so the next scan still compares against it.
func TestTheFranchiseScanJobReportsAFailedFetch(t *testing.T) {
	scan, recorder, accepted := franchiseScanJob(t,
		"https://forge.invalid/guid.foo/fiction-franchises.git", "main")
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	echoTheRun(t, accepted, scan.echo)
	if err := <-done; err == nil {
		t.Fatal("the job succeeded, want it failed on an unreachable forge")
	}

	params := lastRunPosted(t, recorder)
	failure, _ := params[8].(string)
	if !strings.Contains(failure, "forge.invalid") {
		t.Errorf("the run carries the failure %q, want it to name the repository", failure)
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
	url := gitRepository(t, "main", map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile})
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
				url, "main")

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
// Job, so a repository that answered half its files never sweeps a franchise
// off the wall.
func TestTheFranchiseScanPrunesNothingOnACheckoutItCouldNotRead(t *testing.T) {
	scan, agent := franchiseScanner(t,
		gitRepository(t, "main", map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile}),
		"main")
	if err := scan.walkOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	// A second repository that holds neither file the first held, and whose
	// one entry is a directory where a franchise.yaml goes, which every read
	// of it refuses.
	broken := gitRepository(t, "main", map[string]string{"Alien/franchise.yaml/keep": "x"})
	second := franchiseScannerOn(t, scan.catalog, broken, "main")
	err := second.walkOnce(t.Context())

	if !errors.Is(err, errIncompleteWalk) {
		t.Fatalf("the scan ended with %v, want the incomplete walk", err)
	}
	if held := agent.rowsFor(t, "franchises", "house/franchises"); held != 1 {
		t.Errorf("franchises holds %d rows, want the one the last good scan left", held)
	}
}
