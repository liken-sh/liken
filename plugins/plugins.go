// Package plugins installs and manages the per-operator workstation
// CLIs that give the base liken binary its two-layer commands. Each
// operator ships one binary named kubectl-liken-<domain>, published
// beside its operator image at the same version. The base binary
// pulls these binaries out of the registry, keeps them in one
// directory, and reports which are installed and whether any has
// drifted from the operator it faces.
//
// The functions here hold no Kubernetes knowledge. The base binary
// reads the operators from the cluster and hands this package a list
// of domains and images. Everything below is the registry pull, the
// filesystem layout, and the comparison, so the package tests with a
// fake registry and a temporary directory and never a cluster.
package plugins

import (
	"os"
	"path/filepath"
)

// Operator names one operator that ships a CLI: the domain the
// cluster's plugin label gives it, and the operator image whose tag
// names the version its CLI shares.
type Operator struct {
	Domain string
	Image  string
}

// Name returns the file name a domain's CLI installs under. kubectl
// dispatches kubectl liken <domain> by finding this name on PATH, so
// every layer of the design agrees on it.
func Name(domain string) string {
	return "kubectl-liken-" + domain
}

// BinDir returns the directory the plugin CLIs install into, under
// the operator's home. This is the directory the base binary adds to
// PATH so kubectl and the base binary both find the CLIs.
func BinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".liken", "plugins", "bin"), nil
}
