package main

// cli.go parses the command line and runs the server until the pod stops.

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

// builder creates the server. runWith takes it as a parameter so a test
// can replace newServer.
type builder func(context.Context, *config, *slog.Logger) (*server, error)

// run parses args, serves on the CSI socket until ctx ends or the
// process receives SIGINT or SIGTERM, and returns the exit code.
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

// config is the result of parsing the command line.
type config struct {
	endpoint string
	nodeID   string
	store    string
	metrics  string
	// demandMin is how long a demanded pull waits after the last pull
	// of the same repository on this node.
	demandMin time.Duration
	// webhook is the address of the controller's webhook listener. Empty
	// disables the listener.
	webhook string
	// controller selects the Controller service in place of the Node
	// service. Both come from the same binary.
	controller bool
}

// defaultStore is the node plugin's store directory. The controller
// gets the same path because the server creates the store on start and
// the Identity service's Probe checks that it accepts a write. The
// controller never reads a repository under it.
const defaultStore = "/var/lib/liken/pod-storage/git-csi"

const defaultWebhook = ":8080"

// Each subcommand starts one plugin of the binary.
const (
	nodeCommand       = "node"
	controllerCommand = "controller"
)

// parse reads args and writes every problem to out. For --version alone
// it prints the version and returns a nil config and a nil error,
// because there is nothing to serve.
//
// Each subcommand parses its own flags, so the node plugin does not
// accept the controller's flags and the controller does not accept the
// node's.
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
		"unknown subcommand %q, want "+nodeCommand+" or "+controllerCommand, args[0]))
}

// report prints err to out and returns it, so the caller's exit code
// reflects it too.
func report(out io.Writer, err error) error {
	fmt.Fprintln(out, err)
	return err
}

// parseBare handles flags that come before any subcommand. Only
// --version is accepted.
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

// parseNode reads the node plugin's flags. The node plugin owns the
// store and stages every volume that the kubelet asks for.
func parseNode(args []string, out io.Writer) (*config, error) {
	flags := flag.NewFlagSet(nodeCommand, flag.ContinueOnError)
	flags.SetOutput(out)

	endpoint := flags.String("endpoint", "unix:///csi/csi.sock",
		"the address the CSI socket listens on")
	nodeID := flags.String("node-id", "",
		"the name of the node this plugin runs on")
	store := flags.String("store", defaultStore,
		"the directory that holds the repositories and work trees")
	metrics := flags.String("metrics", ":9200",
		"the address the metrics listener serves on; empty disables it")
	// The interval limits a burst of demands on one repository to one pull
	// per interval.
	demandMin := flags.Duration("demand-min-interval", defaultDemandMin,
		"how long a demanded pull waits after the last pull of the same repository")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if *nodeID == "" {
		return nil, report(out, errors.New("--node-id is required"))
	}
	return &config{
		endpoint:  *endpoint,
		nodeID:    *nodeID,
		store:     *store,
		metrics:   *metrics,
		demandMin: *demandMin,
	}, nil
}

// parseController reads the controller plugin's flags. The controller
// does not run for one node and mounts no volumes, so it takes neither
// --node-id nor --store.
func parseController(args []string, out io.Writer) (*config, error) {
	flags := flag.NewFlagSet(controllerCommand, flag.ContinueOnError)
	flags.SetOutput(out)

	endpoint := flags.String("endpoint", "unix:///csi/csi.sock",
		"the address the CSI socket listens on")
	metrics := flags.String("metrics", ":9200",
		"the address the metrics listener serves on; empty disables it")
	// A forge posts to this address through an Ingress that the person who
	// deploys the controller writes.
	webhook := flags.String("webhook", defaultWebhook,
		"the address the webhook listener serves on; empty disables it")

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
