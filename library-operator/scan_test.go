package main

// These tests run the scanner against a broker on a loopback port, so
// the environment it reads, the connection it makes, and the messages
// it leaves behind are all proved with no Mosquitto and no cluster.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

	brokers := make(chan *fakeBroker, 4)
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
func scanEnvironment(t *testing.T, address string) {
	t.Helper()
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(libraryKindVariable, "movies")
	t.Setenv(libraryRootVariable, "/movies")
	t.Setenv(busAddressVariable, address)
	t.Setenv(topicBaseVariable, "")

	flushWas := scanFlushGrace
	t.Cleanup(func() { scanFlushGrace = flushWas })
	scanFlushGrace = 5 * time.Millisecond

	// The webhook binds an ephemeral loopback port, so two tests that
	// each start a scanner never contend for one fixed port.
	webhookWas := webhookAddress
	t.Cleanup(func() { webhookAddress = webhookWas })
	webhookAddress = "127.0.0.1:0"
}

// startScanner runs one scanner and ends it before the test returns,
// so no scanner outlives the test that made it and reads an
// environment the next test has changed.
func startScanner(t *testing.T, scan *scanner) {
	t.Helper()
	stopped, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		scan.serve(stopped)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
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

// waitForTopic reads the broker's publishes until one arrives on the
// topic the test wants, and fails when none does. The scanner
// publishes on two topics, so a test that wants one of them must skip
// the other.
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

// The report is retained and carries zero titles, so the operator
// folds a real report from a scanner that has walked nothing and the
// broker holds it for a subscriber that arrives later.
func TestTheScannerPublishesARetainedReportOfZeroTitlesOnConnect(t *testing.T) {
	address, accepted := testBroker(t)
	scanEnvironment(t, address)
	started := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	startScanner(t, newScanner(started, io.Discard))

	broker := waitForBroker(t, accepted)
	got := waitForTopic(t, broker, "liken/library/libraries/house/movies/status")

	if !got.retained {
		t.Error("the report was not retained")
	}
	var report libraryReport
	if err := json.Unmarshal(got.payload, &report); err != nil {
		t.Fatal(err)
	}
	if report.Titles != 0 || report.Unidentified != 0 {
		t.Errorf("report = %+v, want zero counts", report)
	}
	// The initial walk of a root that holds nothing reports zero titles
	// and no change, so the last-change time stands at the moment the
	// scanner came up while the last-walk time moved to the walk.
	if !report.LastChange.Equal(started) {
		t.Errorf("report last change = %v, want %v", report.LastChange, started)
	}
	if report.LastWalk.IsZero() {
		t.Error("report last walk was not set by the initial walk")
	}
}

// Online is retained, so a subscriber that arrives later reads that
// the scanner is up without waiting for the next message.
func TestTheScannerPublishesRetainedOnlineOnConnect(t *testing.T) {
	address, accepted := testBroker(t)
	scanEnvironment(t, address)

	startScanner(t, newScanner(time.Now().UTC(), io.Discard))

	broker := waitForBroker(t, accepted)
	got := waitForTopic(t, broker, "liken/library/libraries/house/movies/availability")

	if string(got.payload) != availabilityOnline {
		t.Errorf("payload = %q, want %q", got.payload, availabilityOnline)
	}
	if !got.retained {
		t.Error("the availability was not retained")
	}
}

// The one log line names the Library, its kind, and the path inside
// the mount, so the pod's log shows the wiring the Library declares.
func TestTheScannerLogsWhatItWasGiven(t *testing.T) {
	address, _ := testBroker(t)
	scanEnvironment(t, address)
	var logged bytes.Buffer

	newScanner(time.Now().UTC(), &logged)

	line := logged.String()
	if !strings.Contains(line, "house/movies") || !strings.Contains(line, "/library/movies") {
		t.Errorf("log = %q, want the Library and the path inside the mount", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("log = %q, want one line", line)
	}
}

// A Library with no root of its own scans the whole mount, and a
// scanner with no topic base uses the default.
func TestTheScannerFallsBackToTheMountRootAndTheDefaultBase(t *testing.T) {
	address, _ := testBroker(t)
	scanEnvironment(t, address)
	t.Setenv(libraryRootVariable, "")
	var logged bytes.Buffer

	scan := newScanner(time.Now().UTC(), &logged)

	if !strings.Contains(logged.String(), "at /library\n") {
		t.Errorf("log = %q, want the mount root", logged.String())
	}
	if scan.statusTopic != libraryStatusTopic(defaultTopicBase, "house", "movies") {
		t.Errorf("status topic = %q, want the default base", scan.statusTopic)
	}
}

// The scanner reads the ignore list the operator JSON-encodes into the
// environment, so the walk skips the folders the Library declares.
func TestNewScannerReadsTheIgnoreList(t *testing.T) {
	address, _ := testBroker(t)
	scanEnvironment(t, address)
	t.Setenv(libraryIgnoreVariable, `["#recycle",".incoming"]`)

	scan := newScanner(time.Now().UTC(), io.Discard)

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

// The kubelet stops a pod with SIGTERM. The scanner marks itself
// offline and returns, and it leaves the retained report where it is,
// because a library outlives the pod that walked it.
func TestRunScanPublishesOfflineOnSIGTERMAndReturns(t *testing.T) {
	address, accepted := testBroker(t)
	scanEnvironment(t, address)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		runScan()
	}()

	broker := waitForBroker(t, accepted)
	// The online publish proves the scanner is past the point where it
	// registers for the signal, so the signal below always reaches its
	// handler and never the default one, which would end the test
	// binary.
	waitForTopic(t, broker, "liken/library/libraries/house/movies/availability")

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	got := waitForTopic(t, broker, "liken/library/libraries/house/movies/availability")
	if string(got.payload) != availabilityOffline {
		t.Errorf("payload = %q, want %q", got.payload, availabilityOffline)
	}
	if !got.retained {
		t.Error("the closing availability was not retained")
	}
	select {
	case <-returned:
	case <-time.After(scanTestTimeout):
		t.Fatal("runScan did not return after SIGTERM")
	}
}

// serveScanner builds a scanner over a fixture root, wired to the test
// broker and a recording catalog, so serve runs a real walk and a real
// connection with no cluster.
func serveScanner(t *testing.T, address, root, kind string) (*scanner, *catalogRecorder) {
	t.Helper()
	catalog, recorder := recordingCatalog(t)
	now := time.Now().UTC()
	scan := &scanner{
		statusTopic:       libraryStatusTopic(defaultTopicBase, "house", "movies"),
		availabilityTopic: libraryAvailabilityTopic(defaultTopicBase, "house", "movies"),
		root:              root,
		library:           "house/movies",
		kind:              kind,
		catalog:           catalog,
		report:            libraryReport{LastWalk: now, LastChange: now},
	}
	scan.bus = newBus(address, "library-house-movies",
		&busWill{Topic: scan.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		scan.onConnect, nil)
	return scan, recorder
}

// The initial walk runs before the bus connects, so the first report
// the broker holds already carries the volume's title count.
func TestServeRunsTheInitialWalkAndReportsTitles(t *testing.T) {
	webhookWas := webhookAddress
	t.Cleanup(func() { webhookAddress = webhookWas })
	webhookAddress = "127.0.0.1:0"
	flushWas := scanFlushGrace
	t.Cleanup(func() { scanFlushGrace = flushWas })
	scanFlushGrace = 5 * time.Millisecond

	address, accepted := testBroker(t)
	scan, _ := serveScanner(t, address, "testdata/movies", libraryKindMovies)
	startScanner(t, scan)

	broker := waitForBroker(t, accepted)
	got := waitForTopic(t, broker, scan.statusTopic)
	var report libraryReport
	if err := json.Unmarshal(got.payload, &report); err != nil {
		t.Fatal(err)
	}
	if report.Titles != 3 {
		t.Errorf("titles = %d, want the three movies from the initial walk", report.Titles)
	}
}

// The slow timer re-walks the whole root, so a second report reaches
// the broker without any webhook.
func TestServeRepublishesOnTheSlowTimer(t *testing.T) {
	webhookWas := webhookAddress
	t.Cleanup(func() { webhookAddress = webhookWas })
	webhookAddress = "127.0.0.1:0"
	flushWas := scanFlushGrace
	t.Cleanup(func() { scanFlushGrace = flushWas })
	scanFlushGrace = 5 * time.Millisecond
	intervalWas := scanInterval
	t.Cleanup(func() { scanInterval = intervalWas })
	scanInterval = 20 * time.Millisecond

	address, accepted := testBroker(t)
	scan, _ := serveScanner(t, address, "testdata/movies", libraryKindMovies)
	startScanner(t, scan)

	broker := waitForBroker(t, accepted)
	waitForTopic(t, broker, scan.statusTopic)
	waitForTopic(t, broker, scan.statusTopic)
}

// reportOn decodes the report one status publish carried.
func reportOn(t *testing.T, published brokerPublish) libraryReport {
	t.Helper()
	var report libraryReport
	if err := json.Unmarshal(published.payload, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

// A walk publishes its report twice, so the bus carries the walk in
// flight and then the walk done.
func TestAWalkPublishesTheReportAtItsStartAndAtItsEnd(t *testing.T) {
	webhookWas := webhookAddress
	t.Cleanup(func() { webhookAddress = webhookWas })
	webhookAddress = "127.0.0.1:0"
	flushWas := scanFlushGrace
	t.Cleanup(func() { scanFlushGrace = flushWas })
	scanFlushGrace = 5 * time.Millisecond

	address, accepted := testBroker(t)
	scan, _ := serveScanner(t, address, "testdata/movies", libraryKindMovies)
	startScanner(t, scan)

	broker := waitForBroker(t, accepted)
	// onConnect publishes the report on connect, so waiting for it here
	// means the bus is connected and the walk's own publishes below reach
	// the broker.
	waitForTopic(t, broker, scan.statusTopic)

	scan.fullWalk(context.Background())

	started := reportOn(t, waitForTopic(t, broker, scan.statusTopic))
	ended := reportOn(t, waitForTopic(t, broker, scan.statusTopic))

	if !started.Walking {
		t.Error("the report at the start of a walk does not say a walk is in flight")
	}
	if ended.Walking {
		t.Error("the report at the end of a walk still says a walk is in flight")
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

// A path outside the root names no folder, so the rescan falls back to
// a full walk.
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
