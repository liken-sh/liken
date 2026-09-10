package main

// cli.go holds the command line and the run that serves the socket
// until the pod stops.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"syscall"
	"time"
)

// version is the release the binary was built from. The Dockerfile sets
// it with -ldflags "-X main.version=...", and every other build reports
// dev.
var version = "dev"

// builder is how run makes the server. It is a parameter so a test can
// drive a serve that fails.
type builder func(context.Context, *config, *slog.Logger) (*server, error)

// run parses the command line, serves the socket, and returns the exit
// code. The context is the run's life: a signal ends it in the pod, and
// a test ends it the same way.
func run(ctx context.Context, args []string, out io.Writer) int {
	return runWith(ctx, args, out, newServer)
}

// runWith is run with the server's construction named, which is
// the seam a test takes.
func runWith(ctx context.Context, args []string, out io.Writer, build builder) int {
	cfg, err := parse(args, out)
	if err != nil {
		return 1
	}
	if cfg == nil {
		return 0
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server, err := build(ctx, cfg, slog.Default())
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if err := server.serve(ctx); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	return 0
}

// config is what the command line resolves to.
type config struct {
	endpoint string
	nodeID   string
	store    string
	metrics  string
	// sweepEvery is the interval between two passes that look for a copy
	// no PersistentVolume names. A deletion wakes a pass at once, so the
	// tick catches what the watch missed.
	sweepEvery time.Duration
}

// defaultStore is where the node plugin keeps the copies and the holds.
const defaultStore = "/var/lib/liken/pod-storage/per-node"

// defaultSweepEvery is the interval between two sweeps when the command
// line does not set --sweep-every.
const defaultSweepEvery = 10 * time.Minute

// parse reads the command line and writes every problem to out. After
// --version it returns a nil config and no error, because the version
// is printed and nothing is left to run.
func parse(args []string, out io.Writer) (*config, error) {
	flags := flag.NewFlagSet("per-node-csi-driver", flag.ContinueOnError)
	flags.SetOutput(out)

	endpoint := flags.String("endpoint", "unix:///csi/csi.sock",
		"the address the CSI socket listens on")
	nodeID := flags.String("node-id", "",
		"the name of the node this plugin runs on")
	store := flags.String("store", defaultStore,
		"the directory that holds this node's copies")
	// An empty --metrics serves no metrics. 9290 is this driver's port
	// in liken's organization-wide port table, milestone 65.
	metrics := flags.String("metrics", ":9290",
		"the address the metrics listener takes, or empty to serve none")
	sweepEvery := flags.Duration("sweep-every", defaultSweepEvery,
		"how often the driver looks for a copy no PersistentVolume names")
	showVersion := flags.Bool("version", false, "print the version and exit")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if *showVersion {
		fmt.Fprintln(out, version)
		return nil, nil
	}
	if *nodeID == "" {
		return nil, report(out, errors.New("--node-id is required"))
	}
	return &config{
		endpoint:   *endpoint,
		nodeID:     *nodeID,
		store:      *store,
		metrics:    *metrics,
		sweepEvery: *sweepEvery,
	}, nil
}

// report writes the problem where the person who ran the command sees
// it, and hands it back so the exit code says it too.
func report(out io.Writer, err error) error {
	fmt.Fprintln(out, err)
	return err
}
