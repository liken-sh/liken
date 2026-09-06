package main

// cli.go holds the command line and the run that serves the
// socket until the pod stops.

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
	// sweepAfter is how long a work tree nothing stages is kept before
	// the sweep removes it.
	sweepAfter time.Duration
	// controller serves the Controller service in place of the Node
	// service, from the same binary.
	controller bool
}

// parse reads the command line and reports every problem to out. When
// the arguments ask for the version alone, it prints it and answers a
// nil config with a nil error, because there is nothing to run.
func parse(args []string, out io.Writer) (*config, error) {
	flags := flag.NewFlagSet("git-csi-driver", flag.ContinueOnError)
	flags.SetOutput(out)

	endpoint := flags.String("endpoint", "unix:///csi/csi.sock",
		"the address the CSI socket listens on")
	nodeID := flags.String("node-id", "",
		"the name of the node this plugin runs on")
	store := flags.String("store", "/var/lib/liken/pod-storage/git-csi",
		"the directory that holds the repositories and work trees")
	// An empty --metrics serves no metrics.
	metrics := flags.String("metrics", ":9808",
		"the address the metrics listener takes; empty serves none")
	// The node plugin never learns that a PersistentVolume was
	// deleted, so a work tree nothing stages for this long is removed.
	sweepAfter := flags.Duration("sweep-after", defaultSweepAfter,
		"how long a work tree nothing stages is kept before it is removed")
	controller := flags.Bool("controller", false,
		"serve the controller plugin, which validates a class and holds no volume")
	showVersion := flags.Bool("version", false, "print the version and exit")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if *showVersion {
		fmt.Fprintln(out, version)
		return nil, nil
	}
	// The controller runs on no node of its own, so it names none.
	if *nodeID == "" && !*controller {
		err := errors.New("--node-id is required")
		fmt.Fprintln(out, err)
		return nil, err
	}
	return &config{
		endpoint:   *endpoint,
		nodeID:     *nodeID,
		store:      *store,
		metrics:    *metrics,
		sweepAfter: *sweepAfter,
		controller: *controller,
	}, nil
}
