package main

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi/capture"
)

// dockerTimeout bounds each docker command.
const dockerTimeout = 2 * time.Minute

func docker(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return stdout.Bytes(), nil
}

// A container runs indiserver with one driver, and publishes the
// server's port on a free port of the loopback address.
type container struct {
	id, address string
}

func start(image, driver string) (container, error) {
	out, err := docker("run", "-d", "-p", "127.0.0.1::7624", image, driver)
	if err != nil {
		return container{}, err
	}
	c := container{id: strings.TrimSpace(string(out))}
	out, err = docker("port", c.id, "7624/tcp")
	if err != nil {
		c.remove()
		return container{}, err
	}
	c.address, _, _ = strings.Cut(strings.TrimSpace(string(out)), "\n")
	return c, nil
}

// indiVersion reads /etc/indi-versions from the container. docker cp
// works on an image with no shell, such as the indi images, and writes
// the file as a tar stream.
func (c container) indiVersion() (string, error) {
	out, err := docker("cp", c.id+":/etc/indi-versions", "-")
	if err != nil {
		return "", err
	}
	archive := tar.NewReader(bytes.NewReader(out))
	if _, err := archive.Next(); err != nil {
		return "", err
	}
	versions, err := io.ReadAll(archive)
	if err != nil {
		return "", err
	}
	return capture.INDIVersion(versions)
}

func (c container) remove() {
	_, _ = docker("rm", "-f", c.id)
}
