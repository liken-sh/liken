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
	t.Setenv(libraryNameVariable, "films")
	t.Setenv(libraryKindVariable, "films")
	t.Setenv(libraryRootVariable, "/movies")
	t.Setenv(busAddressVariable, address)
	t.Setenv(topicBaseVariable, "")

	flushWas := scanFlushGrace
	t.Cleanup(func() { scanFlushGrace = flushWas })
	scanFlushGrace = 5 * time.Millisecond
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
	got := waitForTopic(t, broker, "liken/library/libraries/house/films/status")

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
	if !report.LastWalk.Equal(started) || !report.LastChange.Equal(started) {
		t.Errorf("report times = (%v, %v), want both %v", report.LastWalk, report.LastChange, started)
	}
}

// Online is retained, so a subscriber that arrives later reads that
// the scanner is up without waiting for the next message.
func TestTheScannerPublishesRetainedOnlineOnConnect(t *testing.T) {
	address, accepted := testBroker(t)
	scanEnvironment(t, address)

	startScanner(t, newScanner(time.Now().UTC(), io.Discard))

	broker := waitForBroker(t, accepted)
	got := waitForTopic(t, broker, "liken/library/libraries/house/films/availability")

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
	if !strings.Contains(line, "house/films") || !strings.Contains(line, "/library/movies") {
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
	if scan.statusTopic != libraryStatusTopic(defaultTopicBase, "house", "films") {
		t.Errorf("status topic = %q, want the default base", scan.statusTopic)
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
	waitForTopic(t, broker, "liken/library/libraries/house/films/availability")

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	got := waitForTopic(t, broker, "liken/library/libraries/house/films/availability")
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
