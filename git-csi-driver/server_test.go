package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// startServer is the fixture every service test uses: a server on a
// socket in a temporary directory, and a client connected to it.
func startServer(t *testing.T, logs io.Writer) *grpc.ClientConn {
	t.Helper()
	dir := t.TempDir()
	return start(t, &config{
		endpoint: "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:   "node-1",
		store:    filepath.Join(dir, "store"),
	}, logs)
}

// start serves the configuration on its socket and stops the server
// when the test ends.
func start(t *testing.T, cfg *config, logs io.Writer) *grpc.ClientConn {
	t.Helper()
	server, err := newServer(cfg, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- server.serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	client, err := grpc.NewClient(cfg.endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestTheServerAnswersOnTheSocket(t *testing.T) {
	client := csi.NewIdentityClient(startServer(t, io.Discard))
	info, err := client.GetPluginInfo(t.Context(), &csi.GetPluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo: %v", err)
	}
	if info.GetName() != driverName {
		t.Errorf("GetPluginInfo named %q, want %q", info.GetName(), driverName)
	}
}

func TestNewServerCreatesTheStore(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store", "deeper")
	start(t, &config{
		endpoint: "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:   "node-1",
		store:    store,
	}, io.Discard)
	if _, err := os.Stat(store); err != nil {
		t.Errorf("the store was not created: %v", err)
	}
}

func TestNewServerReplacesAStaleSocket(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "csi.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatalf("writing the stale socket: %v", err)
	}
	client := csi.NewIdentityClient(start(t, &config{
		endpoint: "unix://" + socket,
		nodeID:   "node-1",
		store:    filepath.Join(dir, "store"),
	}, io.Discard))
	if _, err := client.GetPluginInfo(t.Context(), &csi.GetPluginInfoRequest{}); err != nil {
		t.Errorf("GetPluginInfo: %v", err)
	}
}

func TestNewServerReportsAnEndpointItCannotServe(t *testing.T) {
	dir := t.TempDir()
	busy := filepath.Join(dir, "busy")
	if err := os.MkdirAll(filepath.Join(busy, "occupant"), 0o755); err != nil {
		t.Fatalf("making the occupied directory: %v", err)
	}
	for _, c := range []struct {
		name     string
		endpoint string
	}{
		{name: "another scheme", endpoint: "tcp://127.0.0.1:9000"},
		{name: "no scheme", endpoint: filepath.Join(dir, "csi.sock")},
		{name: "a directory that is not there", endpoint: "unix://" + filepath.Join(dir, "absent", "csi.sock")},
		{name: "a socket that cannot be removed", endpoint: "unix://" + busy},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := newServer(&config{
				endpoint: c.endpoint,
				nodeID:   "node-1",
				store:    filepath.Join(t.TempDir(), "store"),
			}, slog.Default())
			if err == nil {
				t.Errorf("newServer(%q) answered no error", c.endpoint)
			}
		})
	}
}

func TestNewServerReportsAStoreItCannotCreate(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	_, err := newServer(&config{
		endpoint: "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:   "node-1",
		store:    filepath.Join(file, "store"),
	}, slog.Default())
	if err == nil {
		t.Fatal("newServer answered no error for a store under a file")
	}
}

func TestTheServerLogsEveryCall(t *testing.T) {
	logs := &strings.Builder{}
	connection := startServer(t, logs)

	if _, err := csi.NewIdentityClient(connection).
		GetPluginInfo(t.Context(), &csi.GetPluginInfoRequest{}); err != nil {
		t.Fatalf("GetPluginInfo: %v", err)
	}
	if _, err := csi.NewNodeClient(connection).
		NodeExpandVolume(t.Context(), &csi.NodeExpandVolumeRequest{}); err == nil {
		t.Fatal("NodeExpandVolume answered no error")
	}

	for _, want := range []string{
		"rpc=/csi.v1.Identity/GetPluginInfo code=OK",
		"rpc=/csi.v1.Node/NodeExpandVolume code=Unimplemented",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the log is %q, want a line with %q", logs.String(), want)
		}
	}
}

func TestServeStopsWhenTheContextEnds(t *testing.T) {
	dir := t.TempDir()
	server, err := newServer(&config{
		endpoint: "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:   "node-1",
		store:    filepath.Join(dir, "store"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := server.serve(ctx); err != nil {
		t.Errorf("serve: %v", err)
	}
}

func TestServeReportsASocketThatGoesAway(t *testing.T) {
	dir := t.TempDir()
	server, err := newServer(&config{
		endpoint: "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:   "node-1",
		store:    filepath.Join(dir, "store"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	if err := server.listener.Close(); err != nil {
		t.Fatalf("closing the socket: %v", err)
	}
	if err := server.serve(t.Context()); err == nil {
		t.Error("serve answered no error after the socket closed")
	}
}
