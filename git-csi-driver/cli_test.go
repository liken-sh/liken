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
			args: func(t *testing.T) []string {
				return append([]string{"node", "--node-id", "node-1"}, temporaryFlags(t)...)
			},
			code: 0,
		},
		{
			name: "no arguments",
			args: func(*testing.T) []string { return nil },
			code: 1,
		},
		{
			name: "an unknown flag",
			args: func(*testing.T) []string { return []string{"--bogus"} },
			code: 1,
		},
		{
			name: "a subcommand the binary does not serve",
			args: func(*testing.T) []string { return []string{"sidecar"} },
			code: 1,
		},
		{
			name: "a flag of the other subcommand",
			args: func(*testing.T) []string { return []string{"controller", "--node-id", "node-1"} },
			code: 1,
		},
		{
			name: "an endpoint the driver cannot serve",
			args: func(*testing.T) []string {
				return []string{"node", "--node-id", "node-1", "--endpoint", "tcp://127.0.0.1:9000"}
			},
			code: 1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			args := c.args(t)
			if code := run(stopped(t), args, out); code != c.code {
				t.Errorf("run(%q) = %d, want %d (output: %q)", args, code, c.code, out)
			}
		})
	}
}

func TestRunPrintsTheVersion(t *testing.T) {
	out := &bytes.Buffer{}
	run(stopped(t), []string{"--version"}, out)
	if out.String() != version+"\n" {
		t.Errorf("run printed %q, want %q", out, version+"\n")
	}
}

func TestRunSaysNothingOnAGoodCommandLine(t *testing.T) {
	out := &bytes.Buffer{}
	run(stopped(t), append([]string{"node", "--node-id", "node-1"}, temporaryFlags(t)...), out)
	if out.String() != "" {
		t.Errorf("run printed %q, want nothing", out)
	}
}

func TestRunReportsAMissingNodeID(t *testing.T) {
	out := &bytes.Buffer{}
	run(stopped(t), []string{"node"}, out)
	if !strings.Contains(out.String(), "--node-id is required") {
		t.Errorf("run printed %q, want the missing node id reported", out)
	}
}

func TestRunReportsAnUnknownFlag(t *testing.T) {
	out := &bytes.Buffer{}
	run(stopped(t), []string{"--bogus"}, out)
	if !strings.Contains(out.String(), "not defined") {
		t.Errorf("run printed %q, want the unknown flag reported", out)
	}
}

func TestRunReportsAnEndpointItCannotServe(t *testing.T) {
	out := &bytes.Buffer{}
	run(stopped(t), []string{"node", "--node-id", "node-1", "--endpoint", "tcp://127.0.0.1:9000"}, out)
	if !strings.Contains(out.String(), "unix://") {
		t.Errorf("run printed %q, want the endpoint reported", out)
	}
}

func TestRunServesUntilTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	args := append([]string{"node", "--node-id", "node-1"}, temporaryFlags(t)...)
	codes := make(chan int, 1)
	go func() { codes <- run(ctx, args, &bytes.Buffer{}) }()
	cancel()
	if code := <-codes; code != 0 {
		t.Errorf("run = %d, want 0", code)
	}
}

func TestParseDefaultsTheEndpointAndTheStore(t *testing.T) {
	cfg, err := parse([]string{"node", "--node-id", "node-1"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := config{
		endpoint:   "unix:///csi/csi.sock",
		nodeID:     "node-1",
		store:      "/var/lib/liken/pod-storage/git-csi",
		metrics:    ":9808",
		sweepAfter: defaultSweepAfter,
		demandMin:  defaultDemandMin,
	}
	if *cfg != want {
		t.Errorf("parse = %+v, want %+v", *cfg, want)
	}
}

func TestParseTakesEveryFlag(t *testing.T) {
	cfg, err := parse([]string{
		"node",
		"--endpoint", "unix:///run/csi/csi.sock",
		"--node-id", "node-1",
		"--store", "/srv/git-csi",
		"--metrics", "127.0.0.1:9808",
		"--sweep-after", "48h",
		"--demand-min-interval", "30s",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := config{
		endpoint:   "unix:///run/csi/csi.sock",
		nodeID:     "node-1",
		store:      "/srv/git-csi",
		metrics:    "127.0.0.1:9808",
		sweepAfter: 48 * time.Hour,
		demandMin:  30 * time.Second,
	}
	if *cfg != want {
		t.Errorf("parse = %+v, want %+v", *cfg, want)
	}
}

func TestParseAnswersNothingForTheVersion(t *testing.T) {
	cfg, err := parse([]string{"--version"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg != nil {
		t.Errorf("parse = %+v, want no configuration", cfg)
	}
}

func TestRunReportsAServeThatFails(t *testing.T) {
	refused := errors.New("the socket went away")
	out := &bytes.Buffer{}
	code := runWith(t.Context(), append([]string{"node", "--node-id", "node-1"}, temporaryFlags(t)...), out,
		func(ctx context.Context, cfg *config, logger *slog.Logger) (*server, error) {
			serving, err := newServer(ctx, cfg, logger)
			if err != nil {
				return nil, err
			}
			serving.serveOn = func(net.Listener) error { return refused }
			return serving, nil
		})
	if code != 1 {
		t.Errorf("run = %d, want 1", code)
	}
	if !strings.Contains(out.String(), refused.Error()) {
		t.Errorf("run printed %q, want the failure in it", out)
	}
}

func TestTheControllerSubcommandNamesNoNode(t *testing.T) {
	cfg, err := parse([]string{"controller"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := config{
		endpoint:   "unix:///csi/csi.sock",
		store:      defaultStore,
		metrics:    ":9808",
		webhook:    defaultWebhook,
		controller: true,
	}
	if *cfg != want {
		t.Errorf("parse = %+v, want %+v", *cfg, want)
	}
}

func TestTheControllerSubcommandTakesEveryFlag(t *testing.T) {
	cfg, err := parse([]string{
		"controller",
		"--endpoint", "unix:///run/csi/csi.sock",
		"--metrics", "127.0.0.1:9808",
		"--webhook", "127.0.0.1:8080",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := config{
		endpoint:   "unix:///run/csi/csi.sock",
		store:      defaultStore,
		metrics:    "127.0.0.1:9808",
		webhook:    "127.0.0.1:8080",
		controller: true,
	}
	if *cfg != want {
		t.Errorf("parse = %+v, want %+v", *cfg, want)
	}
}

func TestEachSubcommandRefusesTheOtherFlags(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{name: "the node takes no webhook", args: []string{"node", "--node-id", "n", "--webhook", ":8080"}},
		{name: "the controller takes no node id", args: []string{"controller", "--node-id", "n"}},
		{name: "the controller takes no store", args: []string{"controller", "--store", "/srv"}},
		{name: "the controller takes no sweep", args: []string{"controller", "--sweep-after", "1h"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			if _, err := parse(c.args, out); err == nil {
				t.Errorf("parse(%q) answered no error", c.args)
			}
			if !strings.Contains(out.String(), "not defined") {
				t.Errorf("parse printed %q, want the flag reported", out)
			}
		})
	}
}

func TestParseReportsAMissingSubcommand(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no arguments", args: nil, want: "a subcommand is required"},
		{name: "flags alone", args: []string{"--"}, want: "a subcommand is required"},
		{name: "a subcommand the binary does not serve", args: []string{"sidecar"}, want: "is not a subcommand"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			if _, err := parse(c.args, out); err == nil {
				t.Errorf("parse(%q) answered no error", c.args)
			}
			if !strings.Contains(out.String(), c.want) {
				t.Errorf("parse printed %q, want %q in it", out, c.want)
			}
		})
	}
}
