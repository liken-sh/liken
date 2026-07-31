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
	"fmt"
	"io"
	"path/filepath"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/identity"
)

// resolveEndpoint decides where the cluster is: the -server flag
// when given, otherwise the endpoint in the deployment's
// cluster.yaml. A deployment with no cluster.yaml at all is a
// single machine on its own (see cluster.LoadCluster), which never
// declares an endpoint, so that case also asks for -server.
func resolveEndpoint(dir, server string) (string, error) {
	if server != "" {
		return server, nil
	}
	c, err := cluster.LoadCluster(filepath.Join(dir, "cluster.yaml"))
	if err != nil {
		return "", fmt.Errorf("reading the deployment's cluster.yaml: %w", err)
	}
	if c == nil || c.Spec.Endpoint == "" {
		return "", fmt.Errorf("%s/cluster.yaml declares no endpoint; pass -server", dir)
	}
	return c.Spec.Endpoint, nil
}

// writeKubeconfig resolves the endpoint, mints the admin credential
// into the deployment's identity directory, and returns the
// kubeconfig's path.
func writeKubeconfig(dir, server string, out io.Writer) (string, error) {
	endpoint, err := resolveEndpoint(dir, server)
	if err != nil {
		return "", err
	}
	identityDir := filepath.Join(dir, "identity")
	if err := identity.Kubeconfig(identityDir, endpoint, out); err != nil {
		return "", err
	}
	return filepath.Join(identityDir, "kubeconfig"), nil
}
