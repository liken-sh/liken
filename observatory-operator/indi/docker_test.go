// The Docker helpers that the recorder and the integration test share.
// Both run the simulators from the indi-simulators image in the
// topology of plan 03: each driver in its own container behind socat,
// and indiserver in another container, which reaches each driver
// through a link to indi-shim. A restart of a driver's container is
// then a driver that restarts behind the server, as a device pod's
// restart is on a cluster.

package indi

import (
	"bufio"
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dockerTimeout bounds each docker command. A pull of the simulators
// image, about 380 MB, has its own longer bound.
const (
	dockerTimeout = 2 * time.Minute
	pullTimeout   = 10 * time.Minute
)

// docker runs one docker command and returns its trimmed output.
func docker(t *testing.T, timeout time.Duration, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// mustDocker runs one docker command and fails the test when it fails.
func mustDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := docker(t, dockerTimeout, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// simulatorImage names the indi-simulators image at the tag that
// indi/package.toml pins, so the tests follow each bump of the images.
// INDI_SIMULATORS_IMAGE names another image instead, such as one built
// on a workstation. It skips the test when Docker is missing, or when
// the image is neither in the local daemon nor pullable.
func simulatorImage(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped in -short mode: this test runs the simulators in Docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("skipped: no docker command on PATH, and this test runs the simulators in Docker")
	}
	if _, err := docker(t, dockerTimeout, "version"); err != nil {
		t.Skipf("skipped: the Docker daemon does not answer: %v", err)
	}
	image := os.Getenv("INDI_SIMULATORS_IMAGE")
	if image == "" {
		image = "ghcr.io/liken-sh/indi-simulators:" + pinnedTag(t)
	}
	if _, err := docker(t, dockerTimeout, "image", "inspect", image); err == nil {
		return image
	}
	if _, err := docker(t, pullTimeout, "pull", image); err != nil {
		t.Skipf("skipped: %s is not in the local daemon and does not pull: %v", image, err)
	}
	return image
}

// pinnedTag reads <version>-<revision> from indi/package.toml.
func pinnedTag(t *testing.T) string {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "indi", "package.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, seen := values[key]; !seen {
			values[key] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	if values["version"] == "" || values["revision"] == "" {
		t.Fatal("indi/package.toml names no version and revision")
	}
	return values["version"] + "-" + values["revision"]
}

// A rig is one indiserver container and one container for each of its
// drivers, on a Docker network of their own.
type rig struct {
	t       *testing.T
	image   string
	prefix  string
	network string
	server  string
	drivers map[string]string
	// port is the host port that the server's port 7624 is published
	// on. It is fixed, so a client dials the same address after the
	// server's container restarts.
	port string
}

// startRig starts each driver in its own container, then indiserver
// with one shim for each driver, and removes them all when the test
// ends.
func startRig(t *testing.T, image string, drivers ...string) *rig {
	t.Helper()
	prefix := fmt.Sprintf("indi-test-%d", rand.Uint32())
	r := &rig{t: t, image: image, prefix: prefix, network: prefix, drivers: map[string]string{}}
	mustDocker(t, "network", "create", r.network)
	t.Cleanup(r.remove)
	var targets []string
	for _, driver := range drivers {
		name := prefix + "-" + strings.TrimPrefix(driver, "indi_simulator_")
		// socat serves one connection and exits, as in a device pod.
		// The kubelet restarts the pod's container, and Docker's
		// restart policy does the same here.
		mustDocker(t, "run", "-d", "--name", name, "--network", r.network, "--restart", "always",
			"--entrypoint", "/usr/bin/socat", image,
			"TCP-LISTEN:7625,reuseaddr", "EXEC:"+driver+",pipes")
		r.drivers[driver] = name
		targets = append(targets, name+":7625")
	}
	// The image has dash for the catalog program of the CCD simulator,
	// so one shell command makes the links and then runs indiserver,
	// where a cluster uses an init container.
	var links []string
	for _, target := range targets {
		links = append(links, "/tmp/"+target)
	}
	script := fmt.Sprintf("indi-shim link /tmp %s && exec indiserver -r 2147483647 %s",
		strings.Join(targets, " "), strings.Join(links, " "))
	r.server = prefix + "-server"
	r.port = freePort(t)
	// /tmp is a tmpfs, so a restart of the container starts with no
	// links, as a new pod does, and indi-shim link does not refuse
	// the links of the last run.
	mustDocker(t, "run", "-d", "--name", r.server, "--network", r.network, "--tmpfs", "/tmp",
		"-p", "127.0.0.1:"+r.port+":7624", "--entrypoint", "/bin/sh", image, "-c", script)
	return r
}

// freePort finds a port that no process listens on now.
func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return port
}

func (r *rig) address() string { return net.JoinHostPort("127.0.0.1", r.port) }

// restartDriver restarts one driver's container. socat exits, the shim
// reads the end of its connection and exits, and indiserver restarts the
// shim, which connects again once the new socat listens.
func (r *rig) restartDriver(driver string) {
	mustDocker(r.t, "restart", "-t", "0", r.drivers[driver])
}

// restartServer restarts the server's container, which closes every
// client connection.
func (r *rig) restartServer() {
	mustDocker(r.t, "restart", "-t", "0", r.server)
}

func (r *rig) remove() {
	names := []string{r.server}
	for _, name := range r.drivers {
		names = append(names, name)
	}
	if _, err := docker(r.t, dockerTimeout, append([]string{"rm", "-f"}, names...)...); err != nil {
		r.t.Log(err)
	}
	if _, err := docker(r.t, dockerTimeout, "network", "rm", r.network); err != nil {
		r.t.Log(err)
	}
}
