package main

// The scanner is the container that walks one library's volume and
// reports what it holds. It runs beside a Corrosion agent in the pod
// the operator creates for each Library, it mounts the claim read
// only, and it holds no Kubernetes credentials: every fact it reports
// reaches the control plane over the bus, and the operator alone
// writes a Library's status.
//
// The scanner walks nothing. It publishes a report of zero titles and
// holds the pod open, which gives the operator a real report to fold
// and makes the pod, the bus, and the status one working path.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"
)

// scanMode is the argument that selects this role. The operator writes
// it into the scanner container's command, over the image's
// entrypoint, so one image serves the operator and every scanner.
const scanMode = "scan"

// The environment the operator writes into the scanner container. The
// scanner learns which Library it serves from these alone, because the
// pod carries no API credential to look one up with.
const (
	libraryNamespaceVariable = "LIBRARY_NAMESPACE"
	libraryNameVariable      = "LIBRARY_NAME"
	libraryKindVariable      = "LIBRARY_KIND"
	libraryRootVariable      = "LIBRARY_ROOT"
	busAddressVariable       = "LIBRARY_BUS_ADDRESS"
	topicBaseVariable        = "LIBRARY_TOPIC_BASE"
)

// libraryMountPath is where the operator mounts the Library's claim in
// the scanner container. The root from the Library's spec is a path
// inside that mount, so the volume's own layout is what the spec names
// and the mount point is this operator's choice.
const libraryMountPath = "/library"

// scanFlushGrace is how long the scanner holds the bus open after it
// publishes the closing offline, so the writer goroutine drains it
// before the process exits. The message is QoS 0 and carries no
// acknowledgement, so this window is the only signal the scanner has
// that it left. It is a variable so a test drives a shutdown in
// milliseconds.
var scanFlushGrace = 500 * time.Millisecond

// scanner is one scanner container: the two topics it publishes, the
// report it stands behind, and the bus that carries both. The report
// is fixed for the process's life in this plan, so no lock covers it.
type scanner struct {
	statusTopic       string
	availabilityTopic string
	report            libraryReport
	bus               *Bus
}

// runScan is the scan role's whole program. It reads the environment,
// connects to the bus, and holds the pod open until the kubelet stops
// it.
func runScan() {
	// The kernel runs no default action for a signal sent to PID 1, and
	// the scanner is its container's PID 1. The signal context is what
	// ends the wait below, on the kubelet's SIGTERM or on the interrupt
	// a person who runs the binary by hand sends.
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	newScanner(time.Now().UTC(), os.Stdout).serve(stopped)
}

// newScanner reads the container's environment and builds the client
// that speaks for this Library. started is the time the report carries
// as its walk: a scanner that has walked nothing has read the volume
// as of the moment it came up, and the counts it reports are true for
// that moment.
func newScanner(started time.Time, log io.Writer) *scanner {
	namespace := os.Getenv(libraryNamespaceVariable)
	name := os.Getenv(libraryNameVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}
	root := os.Getenv(libraryRootVariable)
	if root == "" {
		root = "/"
	}

	// One line in the pod's log says what this container was given, so
	// a person who reads the pod sees the same wiring the Library
	// declares.
	fmt.Fprintf(log, "library.liken.sh: %s/%s is a %s library at %s\n",
		namespace, name, os.Getenv(libraryKindVariable), path.Join(libraryMountPath, root))

	scan := &scanner{
		statusTopic:       libraryStatusTopic(base, namespace, name),
		availabilityTopic: libraryAvailabilityTopic(base, namespace, name),
		report:            libraryReport{LastWalk: started, LastChange: started},
	}
	// The will is what marks the scanner offline when the pod dies
	// without a chance to publish, which is every kill the kubelet does
	// not ask for first.
	scan.bus = newBus(os.Getenv(busAddressVariable), "library-"+namespace+"-"+name,
		&busWill{Topic: scan.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		scan.onConnect, nil)
	return scan
}

// serve runs the bus until the context ends, then marks this scanner
// offline and returns.
//
// The bus runs on a context of its own rather than the signal context,
// so the closing publish still has a live connection to leave on after
// the signal ends the wait. The retained report stays where it is: a
// library outlives its scanner, and the last walk's report stands
// until the next walk replaces it.
func (s *scanner) serve(stopped context.Context) {
	running, stopBus := context.WithCancel(context.Background())
	go s.bus.Run(running)

	<-stopped.Done()

	s.bus.Publish(s.availabilityTopic, []byte(availabilityOffline), true)
	time.Sleep(scanFlushGrace)
	stopBus()
}

// onConnect refills the broker the moment a session reaches a CONNACK.
// It publishes online and republishes the report, because a broker
// that restarts drops its retained set, and a reconnect has to leave
// the current report behind again.
func (s *scanner) onConnect(bus *Bus) {
	bus.Publish(s.availabilityTopic, []byte(availabilityOnline), true)
	// A libraryReport holds two counts and two times, so it always
	// marshals. There is no failure here for a caller to answer.
	payload, _ := json.Marshal(s.report)
	bus.Publish(s.statusTopic, payload, true)
}
