// record runs INDI drivers under indiserver in Docker and records what
// the server sends. It runs on a workstation after a bump of the INDI
// images, never in a test and never in CI. `make captures` runs it
// twice:
//
//	go run ./indi/capture/record -image IMAGE standard indi/standard
//	go run ./indi/capture/record -image IMAGE simulators indi/testdata
//
// standard runs each bare driver in the image of indi/standard/bare/
// and writes one capture for each, the union of every definition it
// saw. simulators runs each simulator in the indi-simulators image and
// writes the transcripts that the replay tests serve. Names after the
// directory record only those captures or simulators.
//
// Each driver runs in its own container, with indiserver in the same
// container. The record refuses an image whose indi-bin is not the
// version in indi/package.toml, so a capture never describes another
// libindi than the images ship.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi/capture"
)

func main() {
	image := flag.String("image", "", "the image that holds indiserver and the drivers")
	pins := flag.String("pins", filepath.Join("..", "indi", "package.toml"), "the package.toml that pins the INDI version")
	flag.Parse()
	if *image == "" || flag.NArg() < 2 {
		log.Fatal("usage: record -image IMAGE standard|simulators DIRECTORY [NAME...]")
	}
	pinned, err := capture.PinnedINDI(*pins)
	if err != nil {
		log.Fatal(err)
	}
	r := recorder{image: *image, pinned: pinned, dir: flag.Arg(1), only: flag.Args()[2:]}
	switch flag.Arg(0) {
	case "standard":
		err = r.standard()
	case "simulators":
		err = r.simulators()
	default:
		err = fmt.Errorf("record: %q is neither standard nor simulators", flag.Arg(0))
	}
	if err != nil {
		log.Fatal(err)
	}
}

type recorder struct {
	image, pinned, dir string
	only               []string
}

func (r recorder) wanted(name string) bool {
	return len(r.only) == 0 || slices.Contains(r.only, name)
}

// standard records each bare driver. The capture of DefaultDevice comes
// first, and every other capture leaves out the properties it names.
func (r recorder) standard() error {
	for _, bare := range capture.Bares {
		if !r.wanted(bare.Name) {
			continue
		}
		version, device, replies, err := r.run(bare.Driver, bare.Script)
		if err != nil {
			return fmt.Errorf("%s: %w", bare.Name, err)
		}
		kept, err := capture.WriteCapture(r.dir, bare, version, device, replies)
		if err != nil {
			return fmt.Errorf("%s: %w", bare.Name, err)
		}
		log.Printf("%s: %d properties", bare.Name, kept)
	}
	return nil
}

// simulators records each simulator's transcripts, one file for each
// phase.
func (r recorder) simulators() error {
	for _, simulator := range capture.Simulators {
		if !r.wanted(simulator) {
			continue
		}
		script := func(device string) capture.Script { return capture.SimulatorScript(simulator, device) }
		_, device, replies, err := r.run("indi_simulator_"+simulator, script)
		if err != nil {
			return fmt.Errorf("%s: %w", simulator, err)
		}
		if err := capture.WriteTranscripts(filepath.Join(r.dir, simulator), replies); err != nil {
			return err
		}
		log.Printf("%s: %q, %d phases", simulator, device, len(replies))
	}
	return nil
}

// run starts one driver under indiserver in a container of its own,
// checks the container's INDI version against the pin, and runs the
// script against it.
func (r recorder) run(driver string, script func(device string) capture.Script) (string, string, []capture.Reply, error) {
	c, err := start(r.image, driver)
	if err != nil {
		return "", "", nil, err
	}
	defer c.remove()
	version, err := c.indiVersion()
	if err != nil {
		return "", "", nil, err
	}
	if version != r.pinned {
		return "", "", nil, fmt.Errorf("%s holds indi-bin %s, and indi/package.toml pins %s", r.image, version, r.pinned)
	}
	device, replies, err := c.record(script)
	return version, device, replies, err
}

// record waits for the driver, then dials the server again and runs
// the script. When indiserver starts a driver, it asks the driver for
// its properties, and it sends the answer to every client that has
// asked for them. A client that connects in that moment receives every
// definition twice. So a first connection takes the baseline and
// throws it away: once it goes quiet, the driver has answered the
// server. Docker's port forward accepts a connection before indiserver
// listens, and then closes it, so the first connection is dialed again
// until a deadline.
func (c container) record(script func(device string) capture.Script) (string, []capture.Reply, error) {
	baseline := func(string) capture.Script {
		return capture.Script{Phases: []capture.Phase{{Name: "baseline", Message: capture.Baseline}}}
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, _, err := c.dialAndRun(baseline)
		if err == nil {
			break
		}
		if !refused(err) || time.Now().After(deadline) {
			return "", nil, err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return c.dialAndRun(script)
}

func (c container) dialAndRun(script func(device string) capture.Script) (string, []capture.Reply, error) {
	conn, err := net.DialTimeout("tcp", c.address, 5*time.Second)
	if err != nil {
		return "", nil, err
	}
	defer conn.Close()
	return capture.Run(conn, script)
}

func refused(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET)
}
