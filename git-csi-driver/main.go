// git-csi-driver is the CSI driver named git.liken.sh. It mounts git
// repositories as volumes. This file holds the command line; the CSI
// services arrive with plan 01.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// version is the release the binary was built from. The Dockerfile sets
// it with -ldflags "-X main.version=...", and every other build reports
// dev.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, out io.Writer) int {
	if _, err := parse(args, out); err != nil {
		return 1
	}
	// The driver serves nothing yet, so a parsed command line is the
	// whole run.
	return 0
}

// config is what the command line resolves to.
type config struct {
	endpoint string
	nodeID   string
	store    string
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
	store := flags.String("store", "/var/lib/liken/git-csi",
		"the directory that holds the repositories and work trees")
	showVersion := flags.Bool("version", false, "print the version and exit")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if *showVersion {
		fmt.Fprintln(out, version)
		return nil, nil
	}
	if *nodeID == "" {
		err := errors.New("--node-id is required")
		fmt.Fprintln(out, err)
		return nil, err
	}
	return &config{endpoint: *endpoint, nodeID: *nodeID, store: *store}, nil
}
