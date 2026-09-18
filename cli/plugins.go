package main

// The plugins command group installs and manages the per-operator
// CLIs that give liken its two-layer commands. Each operator ships a
// binary named kubectl-liken-<domain> beside its operator image at
// the same version. sync reads the operators the cluster runs, pulls
// each CLI out of the registry, and writes it into the plugin
// directory. list reports what is installed against what the cluster
// runs. remove deletes one. The registry pull, the filesystem layout,
// and the comparison live in the plugins package; this file is the
// cluster read and the dispatch.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/plugins"
)

// clusterClient resolves the deployment's credential and builds a
// client for its cluster, the same resolution the other cluster
// commands use (cluster.go).
func clusterClient(dir, server string) (*kubernetes.Client, error) {
	kubeconfigPath, err := writeKubeconfig(dir, server, io.Discard)
	if err != nil {
		return nil, err
	}
	return kubernetes.KubeconfigClient(kubeconfigPath)
}

// operatorsFromWorkloads maps the plugin workloads to the domains and
// images the sync pulls, dropping any that name no domain or no image.
func operatorsFromWorkloads(workloads []kubernetes.Workload) []plugins.Operator {
	var operators []plugins.Operator
	for _, w := range workloads {
		domain, image := w.PluginDomain(), w.OperatorImage()
		if domain == "" || image == "" {
			continue
		}
		operators = append(operators, plugins.Operator{Domain: domain, Image: image})
	}
	return operators
}

// operatorVersions maps each domain to the version its operator image
// carries, the version every CLI compares against.
func operatorVersions(workloads []kubernetes.Workload) map[string]string {
	versions := map[string]string{}
	for _, w := range workloads {
		domain, image := w.PluginDomain(), w.OperatorImage()
		if domain == "" || image == "" {
			continue
		}
		if version, err := plugins.ImageVersion(image); err == nil {
			versions[domain] = version
		}
	}
	return versions
}

// pluginVersion asks an installed CLI for its stamped version. It is
// a variable so tests observe the call instead of running a real
// binary.
var pluginVersion = execPluginVersion

func execPluginVersion(binDir, domain string) (string, error) {
	out, err := exec.Command(filepath.Join(binDir, plugins.Name(domain)), "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// installedVersions reads the stamped version of each installed CLI.
// A CLI that does not answer --version reports an empty version, so
// the list still names it rather than dropping it.
func installedVersions(binDir string, domains []string) map[string]string {
	versions := map[string]string{}
	for _, domain := range domains {
		version, err := pluginVersion(binDir, domain)
		if err != nil {
			version = ""
		}
		versions[domain] = version
	}
	return versions
}

func pluginsCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: liken plugins sync|list|remove")
	}
	switch args[0] {
	case "sync":
		return pluginsSync(args[1:], out)
	case "list":
		return pluginsList(args[1:], out)
	case "remove":
		return pluginsRemove(args[1:], out)
	default:
		return fmt.Errorf("usage: liken plugins sync|list|remove")
	}
}

func pluginsSync(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("plugins sync", flag.ContinueOnError)
	server := fs.String("server", "", "the API server address, when cluster.yaml's endpoint is not reachable from here")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: liken plugins sync [-server URL] <deployment-dir>")
	}
	c, err := clusterClient(fs.Arg(0), *server)
	if err != nil {
		return err
	}
	workloads, err := kubernetes.ListPluginWorkloads(c)
	if err != nil {
		return err
	}
	binDir, err := plugins.BinDir()
	if err != nil {
		return err
	}
	return plugins.Sync(operatorsFromWorkloads(workloads), runtime.GOARCH, binDir, os.Getenv("PATH"), out)
}

func pluginsList(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("plugins list", flag.ContinueOnError)
	server := fs.String("server", "", "the API server address, when cluster.yaml's endpoint is not reachable from here")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: liken plugins list [-server URL] <deployment-dir>")
	}
	c, err := clusterClient(fs.Arg(0), *server)
	if err != nil {
		return err
	}
	workloads, err := kubernetes.ListPluginWorkloads(c)
	if err != nil {
		return err
	}
	binDir, err := plugins.BinDir()
	if err != nil {
		return err
	}
	domains, err := plugins.Installed(binDir)
	if err != nil {
		return err
	}
	entries := plugins.Merge(installedVersions(binDir, domains), operatorVersions(workloads))
	plugins.Render(entries, out)
	return nil
}

func pluginsRemove(args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: liken plugins remove <domain>")
	}
	binDir, err := plugins.BinDir()
	if err != nil {
		return err
	}
	if err := plugins.Remove(binDir, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed %s\n", plugins.Name(args[0]))
	return nil
}
