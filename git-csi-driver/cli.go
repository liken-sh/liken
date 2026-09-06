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
	"strings"
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
	// demandMin is how long a demanded pull waits after the last pull
	// of the same repository on this node.
	demandMin time.Duration
	// webhook is the address the controller's listener takes. Empty
	// serves none, the way an empty --metrics does.
	webhook string
	// controller serves the Controller service in place of the Node
	// service, from the same binary.
	controller bool
}

// defaultStore is the node plugin's store. The controller takes the
// same path because the Identity service names it, and the controller
// never reads a repository under it.
const defaultStore = "/var/lib/liken/pod-storage/git-csi"

// defaultWebhook is the address the webhook listener takes when the
// command line names none.
const defaultWebhook = ":8080"

// The two plugins the binary serves, one subcommand each.
const (
	nodeCommand       = "node"
	controllerCommand = "controller"
)

// parse reads the command line and reports every problem to out. When
// the arguments ask for the version alone, it prints it and answers a
// nil config with a nil error, because there is nothing to run.
//
// Each subcommand parses its own flags, so no flag of one plugin is
// accepted by the other.
func parse(args []string, out io.Writer) (*config, error) {
	if len(args) == 0 {
		return nil, report(out, errors.New(
			"a subcommand is required: "+nodeCommand+" or "+controllerCommand))
	}
	switch {
	case args[0] == nodeCommand:
		return parseNode(args[1:], out)
	case args[0] == controllerCommand:
		return parseController(args[1:], out)
	case strings.HasPrefix(args[0], "-"):
		return parseBare(args, out)
	}
	return nil, report(out, fmt.Errorf(
		"%q is not a subcommand: "+nodeCommand+" and "+controllerCommand+" are", args[0]))
}

// report writes the problem where the person who ran the command sees
// it, and hands it back so the exit code says it too.
func report(out io.Writer, err error) error {
	fmt.Fprintln(out, err)
	return err
}

// parseBare takes the flags of the command itself, which is --version
// alone.
func parseBare(args []string, out io.Writer) (*config, error) {
	flags := flag.NewFlagSet("git-csi-driver", flag.ContinueOnError)
	flags.SetOutput(out)
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if !*showVersion {
		return nil, report(out, errors.New(
			"a subcommand is required: "+nodeCommand+" or "+controllerCommand))
	}
	fmt.Fprintln(out, version)
	return nil, nil
}

// parseNode takes the node plugin's flags. The node plugin holds the
// store and stages every volume the kubelet asks it for.
func parseNode(args []string, out io.Writer) (*config, error) {
	flags := flag.NewFlagSet(nodeCommand, flag.ContinueOnError)
	flags.SetOutput(out)

	endpoint := flags.String("endpoint", "unix:///csi/csi.sock",
		"the address the CSI socket listens on")
	nodeID := flags.String("node-id", "",
		"the name of the node this plugin runs on")
	store := flags.String("store", defaultStore,
		"the directory that holds the repositories and work trees")
	// An empty --metrics serves no metrics.
	metrics := flags.String("metrics", ":9808",
		"the address the metrics listener takes; empty serves none")
	// The node plugin never learns that a PersistentVolume was
	// deleted, so a work tree nothing stages for this long is removed.
	sweepAfter := flags.Duration("sweep-after", defaultSweepAfter,
		"how long a work tree nothing stages is kept before it is removed")
	// A burst of demands on one repository costs one pull per interval.
	demandMin := flags.Duration("demand-min-interval", defaultDemandMin,
		"how long a demanded pull waits after the last pull of the same repository")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if *nodeID == "" {
		return nil, report(out, errors.New("--node-id is required"))
	}
	return &config{
		endpoint:   *endpoint,
		nodeID:     *nodeID,
		store:      *store,
		metrics:    *metrics,
		sweepAfter: *sweepAfter,
		demandMin:  *demandMin,
	}, nil
}

// parseController takes the controller plugin's flags. The controller
// runs on no node of its own, so it names none. It holds no volume, so
// it takes no store and no sweep.
func parseController(args []string, out io.Writer) (*config, error) {
	flags := flag.NewFlagSet(controllerCommand, flag.ContinueOnError)
	flags.SetOutput(out)

	endpoint := flags.String("endpoint", "unix:///csi/csi.sock",
		"the address the CSI socket listens on")
	metrics := flags.String("metrics", ":9808",
		"the address the metrics listener takes; empty serves none")
	// A forge posts to this address, through an Ingress the person
	// writes.
	webhook := flags.String("webhook", defaultWebhook,
		"the address the webhook listener takes; empty serves none")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	return &config{
		endpoint:   *endpoint,
		store:      defaultStore,
		metrics:    *metrics,
		webhook:    *webhook,
		controller: true,
	}, nil
}
