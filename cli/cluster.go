package main

// This file is the cluster half of the toolkit: how a command on an
// operator's workstation finds the cluster and holds a credential
// for it.
//
// The one positional argument these commands share is the
// deployment directory, the layout GETTING-STARTED.md documents:
// identity sits at <dir>/identity, and the endpoint comes from
// <dir>/cluster.yaml. The endpoint needs a caveat, and -server
// exists because of it. spec.endpoint is the address a follower
// joins through from inside the cluster's own network segment. The
// operator's workstation often cannot reach that address: a dev lab
// reaches its guests through a forwarded localhost port instead.
// So cluster.yaml is the default and -server overrides it.
//
// The kubeconfig goes to a predictable path that these commands
// rewrite and reuse: <dir>/identity/kubeconfig, with 0600
// permissions, beside the identity it is derived from, because that
// is already the directory an operator guards. A fresh file per run
// would leak, because the passthrough commands replace this process
// with the tool (exec), so no deferred cleanup ever runs.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/identity"
	"golang.org/x/sys/unix"
)

// resolveCluster decides where the cluster is and what it is named.
// The endpoint is the -server flag when given, otherwise the
// endpoint in the deployment's cluster.yaml. A deployment with no
// cluster.yaml at all is a single machine on its own (see
// cluster.LoadCluster), which never declares an endpoint, so that
// case also asks for -server. The name comes from the same
// document's metadata, so that the kubeconfig for each cluster an
// operator holds labels its entries with that cluster's own name. A
// deployment with no cluster.yaml has no name, and falls back to
// liken.
func resolveCluster(dir, server string) (name, endpoint string, err error) {
	c, err := cluster.LoadCluster(filepath.Join(dir, "cluster.yaml"))
	if err != nil {
		return "", "", fmt.Errorf("reading the deployment's cluster.yaml: %w", err)
	}
	name = "liken"
	if c != nil && c.Metadata.Name != "" {
		name = c.Metadata.Name
	}
	if server != "" {
		return name, server, nil
	}
	if c == nil || c.Spec.Endpoint == "" {
		return "", "", fmt.Errorf("%s/cluster.yaml declares no endpoint; pass -server", dir)
	}
	return name, c.Spec.Endpoint, nil
}

// writeKubeconfig resolves the cluster's name and endpoint, mints
// the admin credential into the deployment's identity directory, and
// returns the kubeconfig's path.
func writeKubeconfig(dir, server string, out io.Writer) (string, error) {
	name, endpoint, err := resolveCluster(dir, server)
	if err != nil {
		return "", err
	}
	identityDir := filepath.Join(dir, "identity")
	if err := identity.Kubeconfig(identityDir, name, endpoint, out); err != nil {
		return "", err
	}
	return filepath.Join(identityDir, "kubeconfig"), nil
}

// execTool is exec(2): it replaces this process with the tool, so
// the tool owns the terminal and its exit code is liken's. It is a
// variable so tests can observe the call instead of vanishing into
// the exec.
var execTool = unix.Exec

// envWith returns the environment with one variable bound to one
// value, whether or not it was set before. Appending a duplicate
// would not do: when a variable appears twice in an environment,
// which copy a program reads is the program's own accident.
func envWith(environ []string, key, value string) []string {
	kept := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if !strings.HasPrefix(entry, key+"=") {
			kept = append(kept, entry)
		}
	}
	return append(kept, key+"="+value)
}

// passthrough runs one of the tools an operator already uses,
// against the right cluster: it resolves the credential, sets
// KUBECONFIG, and replaces this process with the tool from PATH.
// liken does not reimplement kubectl, and it does not vendor these
// binaries either: every command here is a credential and an
// endpoint handed to a program the operator already chose to
// install.
func passthrough(tool string, args []string) error {
	fs := flag.NewFlagSet(tool, flag.ContinueOnError)
	server := fs.String("server", "", "the API server address, when cluster.yaml's endpoint is not reachable from here")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: liken %s [-server URL] <deployment-dir> [args...]", tool)
	}
	kubeconfigPath, err := writeKubeconfig(fs.Arg(0), *server, io.Discard)
	if err != nil {
		return err
	}
	path, err := exec.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%s is not on PATH; install it and run this again", tool)
	}
	return execTool(path,
		append([]string{tool}, fs.Args()[1:]...),
		envWith(os.Environ(), "KUBECONFIG", kubeconfigPath))
}
