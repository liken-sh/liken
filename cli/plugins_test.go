package main

// These tests cover the plugins command group's cluster read and
// dispatch: mapping Deployments to operators, reading installed
// versions through a seam, and the list and remove commands end to
// end against a fake API server. sync's registry pull is tested in
// the plugins package.

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/plugins"
)

func workloadFixture(domain, image string) kubernetes.Workload {
	var d kubernetes.Workload
	d.Metadata.Labels = map[string]string{kubernetes.PluginLabel: domain}
	d.Spec.Template.Spec.Containers = []kubernetes.WorkloadContainer{{Name: domain + "-operator", Image: image}}
	return d
}

func TestOperatorsFromWorkloadsDropsTheIncomplete(t *testing.T) {
	workloads := []kubernetes.Workload{
		workloadFixture("audio", "ghcr.io/liken-sh/audio-operator:2026.09.03-007"),
		workloadFixture("", "ghcr.io/liken-sh/x:1"),
		workloadFixture("library", ""),
	}
	got := operatorsFromWorkloads(workloads)
	if len(got) != 1 || got[0].Domain != "audio" {
		t.Fatalf("only the complete workload maps to an operator: %+v", got)
	}
}

func TestOperatorVersionsReadsTheTag(t *testing.T) {
	workloads := []kubernetes.Workload{
		workloadFixture("audio", "ghcr.io/liken-sh/audio-operator:2026.09.03-007"),
	}
	got := operatorVersions(workloads)
	if got["audio"] != "2026.09.03-007" {
		t.Fatalf("got %v", got)
	}
}

func TestInstalledVersionsUsesTheSeam(t *testing.T) {
	saved := pluginVersion
	pluginVersion = func(binDir, domain string) (string, error) {
		if domain == "library" {
			return "", fmt.Errorf("no such binary")
		}
		return "2026.09.03-007", nil
	}
	defer func() { pluginVersion = saved }()

	got := installedVersions("/ignored", []string{"audio", "library"})
	if got["audio"] != "2026.09.03-007" {
		t.Errorf("audio: got %q", got["audio"])
	}
	if got["library"] != "" {
		t.Errorf("a CLI that does not answer --version reports no version: got %q", got["library"])
	}
}

func TestPluginsCommandChecksItsSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {"frobnicate"}} {
		if err := pluginsCommand(args, io.Discard); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("args %v must be refused: %v", args, err)
		}
	}
}

func TestPluginsSyncChecksItsArguments(t *testing.T) {
	if err := pluginsSync(nil, io.Discard); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("a missing deployment directory must be refused: %v", err)
	}
}

func TestPluginsRemoveDeletesTheCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".liken", "plugins", "bin")
	installFakePlugin(t, binDir, "audio")

	var out bytes.Buffer
	if err := pluginsCommand([]string{"remove", "audio"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(binDir, plugins.Name("audio"))); !os.IsNotExist(err) {
		t.Fatalf("the CLI must be gone: %v", err)
	}
	if !strings.Contains(out.String(), "audio") {
		t.Fatalf("the confirmation must name the CLI:\n%s", out.String())
	}
}

func TestPluginsRemoveChecksItsArguments(t *testing.T) {
	if err := pluginsRemove(nil, io.Discard); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("remove takes one domain: %v", err)
	}
}

// clusterWorkloadServer answers the plugins commands' workload read.
// The reader lists three kinds; this server returns the given
// workloads under the deployments path and an empty list under the
// other two, so the aggregation over the three calls is under test.
func clusterWorkloadServer(t *testing.T, dir string, workloads ...kubernetes.Workload) *httptest.Server {
	t.Helper()
	serverCert, clientCAs := issueServerCertForTest(t, dir)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		items := "[]"
		if strings.Contains(r.URL.Path, "deployments") {
			items = workloadsJSON(workloads)
		}
		fmt.Fprintf(w, `{"kind":"List","items":%s}`, items)
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func workloadsJSON(workloads []kubernetes.Workload) string {
	var items []string
	for _, d := range workloads {
		items = append(items, fmt.Sprintf(
			`{"metadata":{"name":%q,"labels":{%q:%q}},"spec":{"template":{"spec":{"containers":[{"name":%q,"image":%q}]}}}}`,
			d.Metadata.Name, kubernetes.PluginLabel, d.PluginDomain(),
			d.Metadata.Name, d.OperatorImage()))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestPluginsListReportsInstalledAgainstCluster(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".liken", "plugins", "bin")
	installFakePlugin(t, binDir, "audio")

	saved := pluginVersion
	pluginVersion = func(binDir, domain string) (string, error) { return "2026.09.03-006", nil }
	defer func() { pluginVersion = saved }()

	dir := deploymentDirForTest(t)
	server := clusterWorkloadServer(t, dir,
		workloadFixture("audio", "ghcr.io/liken-sh/audio-operator:2026.09.03-007"),
		workloadFixture("library", "ghcr.io/liken-sh/library-operator:2026.09.03-007"))

	var out bytes.Buffer
	if err := pluginsCommand([]string{"list", "-server", server.URL, dir}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "audio") || !strings.Contains(text, "drift") {
		t.Fatalf("audio's CLI drifted from its operator:\n%s", text)
	}
	if !strings.Contains(text, "library") || !strings.Contains(text, "sync") {
		t.Fatalf("library runs with no local CLI:\n%s", text)
	}
}

func TestPluginsSyncReadsAnEmptyClusterWithoutError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := deploymentDirForTest(t)
	server := clusterWorkloadServer(t, dir)

	var out bytes.Buffer
	if err := pluginsCommand([]string{"sync", "-server", server.URL, dir}, &out); err != nil {
		t.Fatal(err)
	}
}

func TestPluginsListReportsAnUnreachableDeployment(t *testing.T) {
	// A deployment directory with no cluster.yaml and no -server can
	// resolve no cluster, so the command fails before any read.
	if err := pluginsList([]string{t.TempDir()}, io.Discard); err == nil {
		t.Fatal("a deployment that names no cluster must be an error")
	}
}

func TestExecPluginVersionRunsTheBinary(t *testing.T) {
	binDir := t.TempDir()
	script := "#!/bin/sh\necho 2026.09.03-007\n"
	if err := os.WriteFile(filepath.Join(binDir, plugins.Name("audio")), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := execPluginVersion(binDir, "audio")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026.09.03-007" {
		t.Fatalf("got %q", got)
	}
}
