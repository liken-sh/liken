package main

// These tests run the scanner against a catalog loaded with the
// shipped schema, so the environment it reads, the rows it writes, and the
// hand-off it makes are all proved with no cluster.

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scanTestTimeout bounds every wait: long enough that a loaded machine
// still passes, short enough that a broken scanner fails in seconds.
const scanTestTimeout = 5 * time.Second

// testBroker listens on a loopback port and serves every client that
// connects with the same fake broker the bus tests use. The address it
// returns is what the scanner dials, so the test drives the real
// dialer and the real TCP path.
func testBroker(t *testing.T) (address string, accepted <-chan *fakeBroker) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	brokers := make(chan *fakeBroker, 32)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() })
			brokers <- newFakeBroker(conn)
		}
	}()
	return listener.Addr().String(), brokers
}

// scanEnvironment writes the whole environment the operator gives a
// scanner container, so a test reads what the pod would.
func scanEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(libraryKindVariable, "movies")
	t.Setenv(libraryRootVariable, "/movies")
	t.Setenv(jobNameVariable, "scan-1")
	t.Setenv(scanPathVariable, "")
	t.Setenv(handoffTimeoutVariable, "")
}

// MustScanner builds the scanner of the environment the test set.
func mustScanner(t *testing.T, log io.Writer) *scanner {
	t.Helper()
	return newScanner(time.Now().UTC(), log)
}

func waitForBroker(t *testing.T, accepted <-chan *fakeBroker) *fakeBroker {
	t.Helper()
	select {
	case broker := <-accepted:
		return broker
	case <-time.After(scanTestTimeout):
		t.Fatal("the scanner never reached the broker")
		return nil
	}
}

// Reads the broker's publishes until one arrives on the topic the
// test wants, and fails when none does.
func waitForTopic(t *testing.T, broker *fakeBroker, topic string) brokerPublish {
	t.Helper()
	deadline := time.After(scanTestTimeout)
	for {
		select {
		case got := <-broker.pubs:
			if got.topic == topic {
				return got
			}
		case <-deadline:
			t.Fatalf("no publish reached the broker on %q", topic)
			return brokerPublish{}
		}
	}
}

// The scanner reads the Job it runs, the folder it rescans, and
// the wait it gives a confirmer out of the environment, because the pod
// carries no credential to look one up with.
func TestNewScannerReadsTheJobItRuns(t *testing.T) {
	scanEnvironment(t)
	t.Setenv(jobNameVariable, "movies-scan-29128191")
	t.Setenv(scanPathVariable, "/movies/The Thing (1982)")
	t.Setenv(handoffTimeoutVariable, "90s")

	scan := mustScanner(t, io.Discard)

	if scan.job != "movies-scan-29128191" {
		t.Errorf("job = %q, want the Job the environment names", scan.job)
	}
	if len(scan.scanPaths) != 1 || scan.scanPaths[0] != "/movies/The Thing (1982)" {
		t.Errorf("scanPaths = %q, want the folder the environment names", scan.scanPaths)
	}
	if scan.handoffTimeout != 90*time.Second {
		t.Errorf("handoffTimeout = %s, want the wait the environment names", scan.handoffTimeout)
	}
	if scan.worker() != workerRescan {
		t.Errorf("the Job runs the %s worker, want the rescan of this folder", scan.worker())
	}
}

// The one log line names the Library, its kind, and the path inside
// the mount, so the pod's log shows the wiring the Library declares.
func TestTheScannerLogsWhatItWasGiven(t *testing.T) {
	scanEnvironment(t)
	var logged bytes.Buffer

	mustScanner(t, &logged)

	line := logged.String()
	if !strings.Contains(line, "house/movies") || !strings.Contains(line, "/library/movies") {
		t.Errorf("log = %q, want the Library and the path inside the mount", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("log = %q, want one line", line)
	}
}

// A Library with no root of its own scans the whole mount.
func TestTheScannerFallsBackToTheMountRoot(t *testing.T) {
	scanEnvironment(t)
	t.Setenv(libraryRootVariable, "")
	var logged bytes.Buffer

	scan := mustScanner(t, &logged)

	if !strings.Contains(logged.String(), "at /library\n") {
		t.Errorf("log = %q, want the mount root", logged.String())
	}
	if scan.root != libraryMountPath {
		t.Errorf("root = %q, want the mount itself", scan.root)
	}
}

// The scanner reads the ignore list the operator JSON-encodes into the
// environment, so the walk skips the folders the Library declares.
func TestNewScannerReadsTheIgnoreList(t *testing.T) {
	scanEnvironment(t)
	t.Setenv(libraryIgnoreVariable, `["#recycle",".incoming"]`)

	scan := mustScanner(t, io.Discard)

	if !scan.ignore.skips("#recycle") || !scan.ignore.skips(".incoming") {
		t.Errorf("ignore = %v, want both declared folders", scan.ignore)
	}
	if scan.ignore.skips("Action") {
		t.Error("the ignore set skips a folder it was not given")
	}
}

// parseIgnore reads the JSON list into a set, and an empty or
// unreadable value is an empty set the walk skips nothing for.
func TestParseIgnore(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "an empty value is an empty set", raw: "", want: nil},
		{name: "a list of names", raw: `["#recycle",".incoming"]`, want: []string{"#recycle", ".incoming"}},
		{name: "an unreadable value is an empty set", raw: `{`, want: nil},
		{name: "an empty list", raw: `[]`, want: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			set := parseIgnore(testCase.raw)
			for _, name := range testCase.want {
				if !set.skips(name) {
					t.Errorf("set does not skip %q", name)
				}
			}
			if len(set) != len(testCase.want) {
				t.Errorf("set = %v, want %v", set, testCase.want)
			}
		})
	}
}

// A rescan reads the one folder a path names and upserts it, the work a
// webhook drives for a series library.
func TestScannerRescanReadsOneSeriesFolder(t *testing.T) {
	scan, recorder := testScanner(t, "testdata/series", libraryKindSeries)
	episode := filepath.Join("testdata", "series", "Breaking Bad", "Season 02", "Breaking Bad - S02E05.mkv")

	scan.rescan(context.Background(), episode)

	if !postedWith(recorder, "series:tvdb:81189") {
		t.Error("the rescan did not upsert the series")
	}
	if !postedWith(recorder, "episode:tvdb:81189:s02e05") {
		t.Error("the rescan did not upsert the episode")
	}
}

// A rescan reads a title folder at the root, so the direct
// title-folder path is taken and not the grouping one.
func TestScannerRescanReadsARootTitle(t *testing.T) {
	scan, recorder := testScanner(t, "testdata/movies", libraryKindMovies)
	title := filepath.Join("testdata", "movies", "The.Thing.1982.1080p.BluRay.x264-GROUP")

	scan.rescan(context.Background(), title)

	if !postedWith(recorder, "movie:path:the-thing-1982-1080p-bluray-x264-group") {
		t.Error("the rescan did not upsert The Thing")
	}
}

// a rescan logs the path it read and whether it changed anything, so
// a webhook import is legible in the pod log: a folder with content writes,
// and a path that resolves to nothing on the volume and in the catalog is a
// no-change line.
func TestRescanLogsWhetherItChanged(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "a folder with content writes",
			path: filepath.Join("testdata", "movies", "The.Thing.1982.1080p.BluRay.x264-GROUP"),
			want: "wrote",
		},
		{
			name: "a resolved path with nothing to do is a no-change line",
			path: filepath.Join("testdata", "movies", "Ghost (1990)", "ghost.mkv"),
			want: "no change",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			scan, _ := testScanner(t, "testdata/movies", libraryKindMovies)
			var logged bytes.Buffer
			scan.log = &logged

			scan.rescan(context.Background(), testCase.path)

			if !strings.Contains(logged.String(), "rescanned") || !strings.Contains(logged.String(), testCase.want) {
				t.Errorf("log = %q, want a rescan line saying %q", logged.String(), testCase.want)
			}
		})
	}
}

// A path outside the root names no folder, so the rescan falls
// back to a full walk.
func TestScannerRescanFallsBackToAFullWalk(t *testing.T) {
	scan, _ := testScanner(t, "testdata/movies", libraryKindMovies)

	scan.rescan(context.Background(), "/outside/the/root/x.mkv")

	if scan.report.Titles != 3 {
		t.Errorf("titles = %d, want a full walk after an unresolvable path", scan.report.Titles)
	}
}

// A kind with no walk streams nothing, so an unknown kind reports zero
// rather than failing.
func TestWalkOnAnUnknownKindIsEmpty(t *testing.T) {
	scan, _ := testScanner(t, "testdata/movies", "audiobooks")
	folders := 0
	for range scan.walkFolders(context.Background()) {
		folders++
	}
	if folders != 0 {
		t.Errorf("folders = %d, want an empty walk for an unknown kind", folders)
	}
}

// A consumer that stops early ends the folder stream, so a caller that has
// read enough does not read the rest of the volume.
func TestWalkFoldersStopsOnAnEarlyBreak(t *testing.T) {
	cases := []struct {
		name      string
		root      string
		kind      string
		stopAfter int
	}{
		{name: "a grouped movie folder", root: "testdata/movies", kind: libraryKindMovies, stopAfter: 1},
		{name: "a root movie title", root: "testdata/movies", kind: libraryKindMovies, stopAfter: 2},
		{name: "a series folder", root: "testdata/series", kind: libraryKindSeries, stopAfter: 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			scan := &scanner{root: testCase.root, library: "house/library", kind: testCase.kind}
			read := 0
			for range scan.walkFolders(context.Background()) {
				read++
				if read == testCase.stopAfter {
					break
				}
			}
			if read != testCase.stopAfter {
				t.Errorf("read = %d, want the stream to stop after %d", read, testCase.stopAfter)
			}
		})
	}
}

// A cancelled context stops the stream between folders, so a walk of a large
// volume does not run on past a shutdown.
func TestWalkFoldersStopsOnACancelledContext(t *testing.T) {
	scan := &scanner{root: "testdata/movies", library: "house/movies", kind: libraryKindMovies}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	folders := 0
	for range scan.walkFolders(ctx) {
		folders++
	}
	if folders != 0 {
		t.Errorf("folders = %d, want no folder from a cancelled walk", folders)
	}
}

// The webhook resolver reads a path the way the walk reads the volume:
// it steps down through the grouping folders and stops at the first title
// folder. A movies volume that groups by genre and then by studio nests a
// title two levels down, and a resolver that stopped at two levels would
// name the studio and sweep every title under it.
func TestTitleFolderOfFollowsTheWalksOwnRule(t *testing.T) {
	root := titleTree(t,
		filepath.Join("Genre", "Studio", "Nested (2024)"),
		"Root (2020)")
	scan, _ := testScanner(t, root, libraryKindMovies)

	cases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "a file under a title two grouping folders down",
			path: filepath.Join(root, "Genre", "Studio", "Nested (2024)", "movie.mkv"),
			want: filepath.Join(root, "Genre", "Studio", "Nested (2024)"),
		},
		{
			name: "the nested title folder itself",
			path: filepath.Join(root, "Genre", "Studio", "Nested (2024)"),
			want: filepath.Join(root, "Genre", "Studio", "Nested (2024)"),
		},
		{
			name: "a file under a title at the root",
			path: filepath.Join(root, "Root (2020)", "movie.mkv"),
			want: filepath.Join(root, "Root (2020)"),
		},
		{
			name: "a title folder that left the volume",
			path: filepath.Join(root, "Genre", "Studio", "Departed (1999)", "movie.mkv"),
			want: filepath.Join(root, "Genre", "Studio", "Departed (1999)"),
		},
		{
			name: "a grouping folder names no title",
			path: filepath.Join(root, "Genre", "Studio"),
			want: "",
		},
		{
			name: "a path outside the root",
			path: filepath.Join("elsewhere", "Other (2011)", "movie.mkv"),
			want: "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := scan.titleFolderOf(testCase.path); got != testCase.want {
				t.Errorf("titleFolderOf(%q) = %q, want %q", testCase.path, got, testCase.want)
			}
		})
	}
}

// A series folder is always a child of the root, whatever the path names
// under it.
func TestTitleFolderOfASeriesIsTheChildOfTheRoot(t *testing.T) {
	scan, _ := testScanner(t, "testdata/series", libraryKindSeries)
	episode := filepath.Join("testdata", "series", "Breaking Bad", "Season 02", "Breaking Bad - S02E05.mkv")

	if got := scan.titleFolderOf(episode); got != filepath.Join("testdata", "series", "Breaking Bad") {
		t.Errorf("titleFolderOf = %q, want the series folder", got)
	}
}

// One scan Job over a fixture root, writing through a recorder into
// a catalog loaded with the shipped schema, so the run rows, the walk, and
// the hand-off all run with no cluster. The catalog it answers with is the
// one a test writes the confirmation into.
func scanJob(t *testing.T, root, kind, scanPath string) (*scanner, *catalogRecorder, *Catalog) {
	t.Helper()
	catalog, _ := newSQLiteCatalog(t)
	if err := catalog.ensureSeen(t.Context()); err != nil {
		t.Fatal(err)
	}
	recording, recorder := recordingProxy(t, catalog)
	return &scanner{
		root:           root,
		library:        "house/movies",
		kind:           kind,
		catalog:        recording,
		log:            io.Discard,
		job:            "scan-1",
		scanPaths:      scanPathsOf("", scanPath),
		handoffTimeout: scanTestTimeout,
	}, recorder, catalog
}

// The path a webhook reports for one title folder, in the form
// the scanner maps onto the volume.
const webhookFolderPath = "/media/movies/The.Thing.1982.1080p.BluRay.x264-GROUP/" +
	"The.Thing.1982.1080p.BluRay.x264-GROUP.mkv"

// Reads the runs the Job posted, in the order it posted them.
func runsPosted(recorder *catalogRecorder) []capturedStatement {
	var posted []capturedStatement
	for _, statement := range recorder.all() {
		if strings.HasPrefix(statement.sql, "INSERT INTO runs") {
			posted = append(posted, statement)
		}
	}
	return posted
}

// The Job writes its run before it walks and again when the walk
// ends, and the writes that follow name the version a confirmer has to
// hold, so the confirmation proves a standing pod holds everything the
// walk wrote.
func TestTheScanJobWritesItsRunFirstAndLast(t *testing.T) {
	scan, recorder, catalog := scanJob(t, "testdata/movies", libraryKindMovies, "")
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	confirmTheRun(t, catalog, scan.worker(), scan.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	posted := recorder.all()
	if !strings.HasPrefix(posted[0].sql, "INSERT INTO runs") {
		t.Errorf("the first statement was %q, want the run", posted[0].sql)
	}
	if !strings.HasPrefix(posted[len(posted)-1].sql, "INSERT INTO runs") {
		t.Errorf("the last statement was %q, want the run", posted[len(posted)-1].sql)
	}
	runs := runsPosted(recorder)
	if len(runs) != 3 {
		t.Fatalf("the job posted %d runs, want the started one, the finished one, and the write it names", len(runs))
	}
	if runs[0].params[4] != float64(0) {
		t.Errorf("the first run finished at %v, want no finish time", runs[0].params[4])
	}
	if runs[1].params[4] == float64(0) {
		t.Error("the finished run carries no finish time")
	}
	if runs[1].params[1] != workerScan || runs[1].params[2] != "scan-1" {
		t.Errorf("the run names %v/%v, want the scan worker and this Job", runs[1].params[1], runs[1].params[2])
	}
	if runs[2].params[8] != sqliteAgentActor || runs[2].params[9] == float64(0) {
		t.Errorf("the last run names %v at version %v, want the write the finished run made",
			runs[2].params[8], runs[2].params[9])
	}
}

// The finished run carries what the walk read, so a person reads
// the unidentified folders and the sweep off the Library's status.
func TestTheFinishedRunCarriesWhatTheWalkRead(t *testing.T) {
	scan, recorder, catalog := scanJob(t, "testdata/movies", libraryKindMovies, "")
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	confirmTheRun(t, catalog, scan.worker(), scan.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	runs := runsPosted(recorder)
	scan.mutex.Lock()
	unidentified := float64(scan.report.Unidentified)
	scan.mutex.Unlock()
	if runs[1].params[5] != unidentified {
		t.Errorf("the run names %v unidentified folders, want the %v the walk read", runs[1].params[5], unidentified)
	}
}

// The worker a Job's runs row carries: a Job that names a folder is
// the rescan worker, so its row stands beside the full walk's row and
// never overwrites the counts that walk left.
func TestTheWorkerAJobWrites(t *testing.T) {
	cases := []struct {
		name     string
		scanPath string
		want     string
	}{
		{name: "a full walk", want: workerScan},
		{name: "a folder scan", scanPath: webhookFolderPath, want: workerRescan},
		{name: "a folder scan that falls back to the root", scanPath: "/nothing/on/this/volume", want: workerRescan},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			scan, recorder, catalog := scanJob(t, "testdata/movies", libraryKindMovies, testCase.scanPath)
			done := make(chan error, 1)
			go func() { done <- scan.runJob(t.Context()) }()

			confirmTheRun(t, catalog, scan.worker(), scan.job)
			if err := <-done; err != nil {
				t.Fatalf("the job failed: %v", err)
			}

			runs := runsPosted(recorder)
			if len(runs) != 3 {
				t.Fatalf("the job posted %d runs, want the two writes of the walk and the write it names", len(runs))
			}
			for _, run := range runs {
				if run.params[1] != testCase.want {
					t.Errorf("the run names the %v worker, want %v", run.params[1], testCase.want)
				}
			}
		})
	}
}

// A Job that names a folder rescans that folder alone, which is
// what a webhook drives, and it never walks the whole root.
func TestTheScanJobRescansTheFolderItIsGiven(t *testing.T) {
	scan, recorder, catalog := scanJob(t, "testdata/movies", libraryKindMovies, webhookFolderPath)
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	confirmTheRun(t, catalog, scan.worker(), scan.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	if !postedWith(recorder, "movie:path:the-thing-1982-1080p-bluray-x264-group") {
		t.Error("the job did not upsert the folder it was given")
	}
	if scan.report.Titles != 0 {
		t.Errorf("the job read %d titles, want the one folder it was given and no walk of the root", scan.report.Titles)
	}
}

// A folder that maps onto nothing on the volume falls back to the
// whole root, so a Job is never worse than a full walk.
func TestTheScanJobFallsBackToTheWholeRoot(t *testing.T) {
	scan, _, catalog := scanJob(t, "testdata/movies", libraryKindMovies, "/nothing/on/this/volume")
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	confirmTheRun(t, catalog, scan.worker(), scan.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	if scan.report.Titles != 3 {
		t.Errorf("the job read %d titles, want the three the root holds", scan.report.Titles)
	}
}

// A confirmation that never arrives fails the Job, so its rows stay
// on its own claim and Kubernetes retries it.
func TestTheScanJobFailsWithNoConfirmation(t *testing.T) {
	scan, _, _ := scanJob(t, "testdata/movies", libraryKindMovies, "")
	scan.handoffTimeout = 20 * time.Millisecond

	err := scan.runJob(t.Context())

	if err == nil {
		t.Fatal("the job returned no error, want the timeout")
	}
	if !strings.Contains(err.Error(), "scan-1") {
		t.Errorf("error = %v, want the Job named", err)
	}
}

// A walk that fails still writes its finished run and still waits
// to be confirmed, so the failure is visible in the run, and the Job then
// fails.
func TestAFailedWalkStillWritesItsRunAndWaits(t *testing.T) {
	scan, recorder, catalog := scanJob(t, "testdata/movies", libraryKindMovies, "")
	scan.root = filepath.Join(t.TempDir(), "gone")
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	confirmTheRun(t, catalog, scan.worker(), scan.job)
	err := <-done

	if err == nil {
		t.Fatal("the job returned no error, want the failed walk")
	}
	runs := runsPosted(recorder)
	if len(runs) != 3 {
		t.Fatalf("the job posted %d runs, want the two writes of the walk and the write it names", len(runs))
	}
	if runs[1].params[4] == float64(0) {
		t.Error("the run of a failed walk carries no finish time")
	}
}

// A catalog that refuses the first run fails the Job before it
// walks, because a Job whose run never landed waits for a confirmation
// that cannot come.
func TestTheScanJobFailsWhenItCannotWriteItsRun(t *testing.T) {
	unwell := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(unwell.Close)
	scan, _, _ := scanJob(t, "testdata/movies", libraryKindMovies, "")
	scan.catalog = NewCatalog(unwell.URL, unwell.Client())

	if err := scan.runJob(t.Context()); err == nil {
		t.Error("the job returned no error, want the refused run")
	}
}

// A catalog that refuses the finished run fails the Job, so a walk
// whose run never landed is a Job that failed and not one that waits for a
// confirmation that cannot come.
func TestTheScanJobFailsWhenItCannotWriteItsFinishedRun(t *testing.T) {
	catalog, _ := recordingCatalog(t)
	writes := 0
	scan, _, _ := scanJob(t, "testdata/movies", libraryKindMovies, "")
	scan.catalog = proxyCatalog(t, catalog, func(path string, body []byte) bool {
		if !bytes.Contains(body, []byte("INSERT INTO runs")) {
			return false
		}
		writes++
		return writes == 2
	})

	err := scan.runJob(t.Context())

	if err == nil {
		t.Fatal("the job returned no error, want the refused run")
	}
	if !strings.Contains(err.Error(), "finished run") {
		t.Errorf("error = %v, want the finished run named", err)
	}
}

// A title folder whose name opens with dots is a title folder, not a path
// that climbed above the root. A webhook rescan of it reads that one folder,
// where a full walk of the library would cost a read of every folder it
// holds.
func TestTitleFolderOfATitleWhoseNameOpensWithDots(t *testing.T) {
	root := t.TempDir()
	title := "...And Justice for All (1979)"
	writeFile(t, filepath.Join(root, title, "movie.mkv"), "x")

	folder, held := titleFolderOf(root, libraryKindMovies,
		filepath.Join(root, title, "movie.mkv"))

	if !held || folder != filepath.Join(root, title) {
		t.Errorf("titleFolderOf = %q %v, want the title's own folder", folder, held)
	}
}
