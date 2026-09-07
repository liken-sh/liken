package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stopped is a context that is already over, so run serves and stops
// without waiting for a signal.
func stopped(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

// temporaryFlags puts the socket and the store in a temporary directory
// of the test's own.
func temporaryFlags(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	return []string{
		"--node-id", "node-1",
		"--endpoint", "unix://" + filepath.Join(dir, "csi.sock"),
		"--store", filepath.Join(dir, "store"),
		// A test serves no metrics, because two tests would take the same
		// port.
		"--metrics", "",
	}
}

func TestRunExitCodes(t *testing.T) {
	for _, c := range []struct {
		name string
		args func(t *testing.T) []string
		code int
	}{
		{
			name: "the version alone",
			args: func(*testing.T) []string { return []string{"--version"} },
			code: 0,
		},
		{
			name: "a node id and a place to serve",
			args: temporaryFlags,
			code: 0,
		},
		{
			name: "no node id",
			args: func(*testing.T) []string { return []string{"--metrics", ""} },
			code: 1,
		},
		{
			name: "a flag the driver does not take",
			args: func(*testing.T) []string { return []string{"--forge", "somewhere"} },
			code: 1,
		},
		{
			name: "an endpoint that is not a Unix socket",
			args: func(t *testing.T) []string {
				return append(temporaryFlags(t)[:0:0],
					"--node-id", "node-1", "--endpoint", "tcp://127.0.0.1:9000")
			},
			code: 1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			if code := run(stopped(t), c.args(t), out); code != c.code {
				t.Errorf("run answered %d with %q, want %d", code, out, c.code)
			}
		})
	}
}

func TestTheVersionIsWhatTheBinaryWasBuiltFrom(t *testing.T) {
	out := &bytes.Buffer{}
	if code := run(stopped(t), []string{"--version"}, out); code != 0 {
		t.Fatalf("run answered %d, want 0", code)
	}
	if strings.TrimSpace(out.String()) != version {
		t.Errorf("run printed %q, want %q", out, version)
	}
}

func TestTheFlagsCarryTheDefaultsTheManualNames(t *testing.T) {
	cfg, err := parse([]string{"--node-id", "node-1"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.endpoint != "unix:///csi/csi.sock" {
		t.Errorf("--endpoint defaults to %q, want unix:///csi/csi.sock", cfg.endpoint)
	}
	if cfg.store != defaultStore {
		t.Errorf("--store defaults to %q, want %q", cfg.store, defaultStore)
	}
	if cfg.metrics != ":9808" {
		t.Errorf("--metrics defaults to %q, want :9808", cfg.metrics)
	}
	if cfg.sweepEvery != defaultSweepEvery {
		t.Errorf("--sweep-every defaults to %v, want %v", cfg.sweepEvery, defaultSweepEvery)
	}
}

func TestTheFlagsTakeWhatTheCommandLineNames(t *testing.T) {
	cfg, err := parse([]string{
		"--node-id", "node-2",
		"--endpoint", "unix:///run/csi.sock",
		"--store", "/var/lib/example",
		"--metrics", ":9000",
		"--sweep-every", "15s",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := config{
		endpoint:   "unix:///run/csi.sock",
		nodeID:     "node-2",
		store:      "/var/lib/example",
		metrics:    ":9000",
		sweepEvery: 15 * time.Second,
	}
	if *cfg != want {
		t.Errorf("parse answered %+v, want %+v", *cfg, want)
	}
}

func TestACommandLineWithNoNodeIdSaysSo(t *testing.T) {
	out := &bytes.Buffer{}
	if _, err := parse([]string{}, out); err == nil {
		t.Fatal("parse answered no error, want one")
	}
	if !strings.Contains(out.String(), "--node-id is required") {
		t.Errorf("parse printed %q, want it to name --node-id", out)
	}
}

func TestAServerThatCannotBeBuiltEndsTheRun(t *testing.T) {
	out := &bytes.Buffer{}
	failing := func(context.Context, *config, *slog.Logger) (*server, error) {
		return nil, errors.New("the socket was taken")
	}
	if code := runWith(stopped(t), temporaryFlags(t), out, failing); code != 1 {
		t.Errorf("runWith answered %d, want 1", code)
	}
	if !strings.Contains(out.String(), "the socket was taken") {
		t.Errorf("runWith printed %q, want the reason in it", out)
	}
}

func TestAServeThatFailsEndsTheRun(t *testing.T) {
	out := &bytes.Buffer{}
	failing := func(context.Context, *config, *slog.Logger) (*server, error) {
		return &server{
			serveOn:  func(listener net.Listener) error { return errors.New("the socket went away") },
			stop:     func() {},
			readings: newMetrics(),
			logger:   quietLogger(),
		}, nil
	}
	if code := runWith(stopped(t), temporaryFlags(t), out, failing); code != 1 {
		t.Errorf("runWith answered %d, want 1", code)
	}
	if !strings.Contains(out.String(), "the socket went away") {
		t.Errorf("runWith printed %q, want the reason in it", out)
	}
}
