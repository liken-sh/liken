package main

// The serve mode runs indiserver and keeps its drivers equal to a list
// of device addresses in a file. A device can join or leave a telescope
// while a session runs, and a restart of indiserver would disconnect
// every other device on it and end an exposure in progress. indiserver
// reads `start <driver>` and `stop <driver>` lines from the fifo that
// its -f option names, so the shim starts and stops one driver on the
// running server instead.
//
// observatory-operator writes the list as an annotation on the server's
// pod, and the pod mounts the annotation as a file through a downward
// API volume. The kubelet writes the file again when the annotation
// changes, and the shim reads it on each inotify event of its
// directory. The annotation is metadata, so the pod's spec does not
// change when the devices change, and the operator does not replace the
// pod.
//
// The shim is indiserver's parent, and it exits when indiserver exits.
// The kubelet then starts the container again, and the shim starts a
// new indiserver with every driver of the file. So the shim's record of
// the running drivers always describes the indiserver it started.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// serveConfig holds the paths of one server.
type serveConfig struct {
	// self is the shim's own binary, which each link names.
	self string
	// drivers is the file that lists one device address on each line.
	drivers string
	// links is the directory of the links that indiserver starts.
	links string
	// fifo is the fifo that indiserver reads commands from.
	fifo string
	// server is indiserver's command line, without -f.
	server []string
	// output receives indiserver's stdout and stderr.
	output io.Writer
}

// serve starts indiserver and keeps its drivers equal to the file
// until indiserver exits or ctx ends.
func serve(ctx context.Context, c serveConfig) error {
	if err := clearDir(c.links); err != nil {
		return err
	}
	if err := syscall.Mkfifo(c.fifo, 0o600); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("making the fifo %s: %w", c.fifo, err)
	}
	// Linux opens a fifo for reading and writing at once, with no wait
	// for a reader. A write-only open would block until indiserver
	// opens its end. indiserver also closes and opens its end again
	// each time a writer closes, and this end stays open, so no
	// command is lost in that gap.
	fifo, err := os.OpenFile(c.fifo, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer fifo.Close()

	// The watch opens before the first read of the file, so a change
	// that the kubelet writes during the read wakes another read.
	changes, err := watchDir(ctx, filepath.Dir(c.drivers))
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, c.server[0], append(slices.Clone(c.server[1:]), "-f", c.fifo)...)
	cmd.Stdout, cmd.Stderr = c.output, c.output
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	d := &drivers{self: c.self, links: c.links, fifo: fifo, log: c.output, running: map[string]bool{}}
	for {
		if err := d.apply(readDrivers(c.drivers, c.output)); err != nil {
			return err
		}
		select {
		case err := <-exited:
			return fmt.Errorf("indiserver exited: %w", err)
		case _, open := <-changes:
			if !open {
				return fmt.Errorf("the watch of %s ended", filepath.Dir(c.drivers))
			}
		}
	}
}

// clearDir removes every entry of a directory. The links directory is
// an emptyDir, which keeps the links of a container that the kubelet
// started earlier, and a stale link would name a device that left.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// readDrivers reads the device addresses of the file, in its order. A
// missing file is an empty list: the downward API writes an empty file
// for a missing annotation, and the kubelet replaces the file with a
// rename. A line that names no address is logged and skipped, so one
// bad line stops no other driver.
func readDrivers(path string, log io.Writer) []string {
	body, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(log, "indi-shim: reading %s: %v\n", path, err)
		}
		return nil
	}
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if _, err := target(line); err != nil {
			fmt.Fprintf(log, "indi-shim: %s: %v\n", path, err)
			continue
		}
		if !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

// drivers is the set of drivers that the shim started on its
// indiserver, by device address.
type drivers struct {
	self, links string
	fifo        io.Writer
	log         io.Writer
	running     map[string]bool
}

// apply stops each running driver that the list does not name, and
// starts each driver of the list that does not run. indiserver names a
// driver by the path it starts, and `stop` matches that path exactly,
// so both commands name the link's full path.
func (d *drivers) apply(addresses []string) error {
	for _, address := range slices.Sorted(maps.Keys(d.running)) {
		if slices.Contains(addresses, address) {
			continue
		}
		path := filepath.Join(d.links, address)
		if err := d.command("stop", path); err != nil {
			return err
		}
		// indiserver ends the driver when it reads the stop, and never
		// starts it again, so the link is no longer needed.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(d.log, "indi-shim: %v\n", err)
		}
		delete(d.running, address)
	}
	for _, address := range addresses {
		if d.running[address] {
			continue
		}
		path := filepath.Join(d.links, address)
		if err := os.Symlink(d.self, path); err != nil && !errors.Is(err, os.ErrExist) {
			fmt.Fprintf(d.log, "indi-shim: %v\n", err)
			continue
		}
		if err := d.command("start", path); err != nil {
			return err
		}
		d.running[address] = true
	}
	return nil
}

// command writes one line to indiserver's fifo. A write of less than
// PIPE_BUF bytes is atomic, so indiserver never reads half a line.
func (d *drivers) command(verb, path string) error {
	fmt.Fprintf(d.log, "indi-shim: %s %s\n", verb, path)
	_, err := io.WriteString(d.fifo, verb+" "+path+"\n")
	return err
}
