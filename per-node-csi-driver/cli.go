package main

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

// The Dockerfile sets version with -ldflags "-X main.version=...".
var version = "dev"

// builder is a parameter of runWith so a test can pass a server that
// fails.
type builder func(context.Context, *config, *slog.Logger) (*server, error)

func run(ctx context.Context, args []string, out io.Writer) int {
	return runWith(ctx, args, out, newServer)
}

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

type config struct {
	endpoint   string
	nodeID     string
	store      string
	metrics    string
	sweepEvery time.Duration
}

const defaultStore = "/var/lib/liken/pod-storage/per-node"

const defaultSweepEvery = 10 * time.Minute

// parse writes flag errors and the version to out. With --version it
// returns a nil config and a nil error, which means there is nothing to
// run.
func parse(args []string, out io.Writer) (*config, error) {
	flags := flag.NewFlagSet("per-node-csi-driver", flag.ContinueOnError)
	flags.SetOutput(out)

	endpoint := flags.String("endpoint", "unix:///csi/csi.sock",
		"the address of the CSI socket, in the form unix:///path")
	nodeID := flags.String("node-id", "",
		"the name of the node this driver runs on")
	store := flags.String("store", defaultStore,
		"the directory of this node's copies and mount records")
	// liken components serve their metrics on port 9200 by convention.
	metrics := flags.String("metrics", ":9200",
		"the address the metrics listener binds, or empty to serve no metrics")
	sweepEvery := flags.Duration("sweep-every", defaultSweepEvery,
		"how often the driver deletes copies that no PersistentVolume names")
	showVersion := flags.Bool("version", false, "print the version and exit")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if *showVersion {
		fmt.Fprintln(out, version)
		return nil, nil
	}
	if *nodeID == "" {
		return nil, report(out, errors.New("--node-id is required: set it to the name of the node this driver runs on"))
	}
	return &config{
		endpoint:   *endpoint,
		nodeID:     *nodeID,
		store:      *store,
		metrics:    *metrics,
		sweepEvery: *sweepEvery,
	}, nil
}

func report(out io.Writer, err error) error {
	fmt.Fprintln(out, err)
	return err
}
