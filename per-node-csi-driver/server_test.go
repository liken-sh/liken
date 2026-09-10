package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// startServer is the fixture every service test uses: a server on a
// socket in a temporary directory, and a client connected to it.
func startServer(t *testing.T, logs io.Writer) *grpc.ClientConn {
	t.Helper()
	dir := t.TempDir()
	return start(t, &config{
		endpoint:   "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:     "node-1",
		store:      filepath.Join(dir, "store"),
		sweepEvery: time.Hour,
	}, logs)
}

// start serves the configuration on its socket and stops the server
// when the test ends.
func start(t *testing.T, cfg *config, logs io.Writer) *grpc.ClientConn {
	t.Helper()
	server, err := newServer(t.Context(), cfg, slog.New(slog.NewTextHandler(logs, nil)))
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

func TestTheServerRegistersTheNodeServiceAndNoControllerService(t *testing.T) {
	connection := startServer(t, io.Discard)
	if _, err := csi.NewNodeClient(connection).NodeGetInfo(
		t.Context(), &csi.NodeGetInfoRequest{}); err != nil {
		t.Errorf("the server serves no Node service: %v", err)
	}
	_, err := csi.NewControllerClient(connection).ControllerGetCapabilities(
		t.Context(), &csi.ControllerGetCapabilitiesRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("the server answered %v, want Unimplemented from the Controller service", err)
	}
}

func TestOperationNameIsTheGRPCMethodAfterItsLastSlash(t *testing.T) {
	for _, c := range []struct {
		fullMethod string
		want       string
	}{
		{"/csi.v1.Node/NodePublishVolume", "NodePublishVolume"},
		{"/csi.v1.Identity/GetPluginInfo", "GetPluginInfo"},
		{"NodeGetInfo", "NodeGetInfo"},
	} {
		if got := operationName(c.fullMethod); got != c.want {
			t.Errorf("operationName(%q) = %q, want %q", c.fullMethod, got, c.want)
		}
	}
}

func TestMetricsInterceptorRecordsTheCallUnderItsOperationName(t *testing.T) {
	readings := newMetrics()
	call := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Node/NodePublishVolume"}
	handle := func(ctx context.Context, request any) (any, error) { return "answer", nil }
	answer, err := metricsInterceptor(readings)(t.Context(), nil, call, handle)
	if err != nil || answer != "answer" {
		t.Fatalf("the interceptor answered (%v, %v), want (answer, nil)", answer, err)
	}
	if got := testutil.ToFloat64(readings.reconcileErrors.WithLabelValues("NodePublishVolume")); got != 0 {
		t.Errorf("pernodecsi_reconcile_errors_total reads %v, want 0 for a call with no error", got)
	}
}

func TestMetricsInterceptorCountsAnErrorUnderItsOperationName(t *testing.T) {
	readings := newMetrics()
	call := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Node/NodePublishVolume"}
	failed := errors.New("refused")
	handle := func(ctx context.Context, request any) (any, error) { return nil, failed }
	if _, err := metricsInterceptor(readings)(t.Context(), nil, call, handle); !errors.Is(err, failed) {
		t.Fatalf("the interceptor answered %v, want %v", err, failed)
	}
	if got := testutil.ToFloat64(readings.reconcileErrors.WithLabelValues("NodePublishVolume")); got != 1 {
		t.Errorf("pernodecsi_reconcile_errors_total reads %v, want 1", got)
	}
}

func TestTheServerRecordsTheReconcileMetricPerCall(t *testing.T) {
	dir := t.TempDir()
	cfg := &config{
		endpoint:   "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:     "node-1",
		store:      filepath.Join(dir, "store"),
		metrics:    "127.0.0.1:0",
		sweepEvery: time.Hour,
	}
	served, err := newServer(t.Context(), cfg, quietLogger())
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- served.serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-stopped; err != nil {
			t.Errorf("serve: %v", err)
		}
	})

	client, err := grpc.NewClient(cfg.endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	if _, err := csi.NewIdentityClient(client).GetPluginInfo(t.Context(), &csi.GetPluginInfoRequest{}); err != nil {
		t.Fatalf("GetPluginInfo: %v", err)
	}

	body := fetch(t, "http://"+served.metrics.Addr().String()+"/metrics")
	if !strings.Contains(body, `pernodecsi_reconcile_duration_seconds_count{kind="GetPluginInfo"} 1`) {
		t.Errorf("the metrics listener answered %q, want one observation under GetPluginInfo", body)
	}
}

func TestTheServerWritesOneLinePerCall(t *testing.T) {
	written := &bytes.Buffer{}
	client := csi.NewIdentityClient(startServer(t, written))
	if _, err := client.GetPluginInfo(t.Context(), &csi.GetPluginInfoRequest{}); err != nil {
		t.Fatalf("GetPluginInfo: %v", err)
	}
	if !strings.Contains(written.String(), "GetPluginInfo") {
		t.Errorf("the log reads %q, want the call in it", written)
	}
}

func TestTheServerTakesTheSocketAKilledPodLeftBehind(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "csi.sock")
	if err := os.WriteFile(socket, []byte("a dead pod's socket\n"), 0o600); err != nil {
		t.Fatalf("writing the socket file: %v", err)
	}
	client := csi.NewIdentityClient(start(t, &config{
		endpoint:   "unix://" + socket,
		nodeID:     "node-1",
		store:      filepath.Join(dir, "store"),
		sweepEvery: time.Hour,
	}, io.Discard))
	if _, err := client.GetPluginInfo(t.Context(), &csi.GetPluginInfoRequest{}); err != nil {
		t.Errorf("GetPluginInfo: %v", err)
	}
}

func TestTheServerServesTheMetricsListenerBesideTheSocket(t *testing.T) {
	dir := t.TempDir()
	client := csi.NewIdentityClient(start(t, &config{
		endpoint:   "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:     "node-1",
		store:      filepath.Join(dir, "store"),
		metrics:    "127.0.0.1:0",
		sweepEvery: time.Hour,
	}, io.Discard))
	if _, err := client.GetPluginInfo(t.Context(), &csi.GetPluginInfoRequest{}); err != nil {
		t.Errorf("GetPluginInfo: %v", err)
	}
}

func TestAServerThatCannotStartSaysWhy(t *testing.T) {
	for _, c := range []struct {
		name    string
		make    func(t *testing.T) *config
		message string
	}{
		{
			name: "an endpoint that is not a Unix socket",
			make: func(t *testing.T) *config {
				return &config{endpoint: "tcp://127.0.0.1:9000", store: t.TempDir()}
			},
			message: "does not begin with unix://",
		},
		{
			name: "a store the driver cannot make",
			make: func(t *testing.T) *config {
				root := filepath.Join(t.TempDir(), "store")
				if err := os.WriteFile(root, []byte("not a directory\n"), 0o600); err != nil {
					t.Fatalf("writing the file: %v", err)
				}
				return &config{endpoint: "unix://" + filepath.Join(t.TempDir(), "csi.sock"), store: root}
			},
			message: "not a directory",
		},
		{
			name: "a socket path the driver cannot clear",
			make: func(t *testing.T) *config {
				dir := t.TempDir()
				socket := filepath.Join(dir, "csi.sock")
				if err := os.MkdirAll(filepath.Join(socket, "inner"), 0o755); err != nil {
					t.Fatalf("making the directory: %v", err)
				}
				return &config{endpoint: "unix://" + socket, store: filepath.Join(dir, "store")}
			},
			message: "directory not empty",
		},
		{
			name: "a socket path the kernel refuses",
			make: func(t *testing.T) *config {
				dir := t.TempDir()
				return &config{
					endpoint: "unix://" + filepath.Join(dir, "absent", "csi.sock"),
					store:    filepath.Join(dir, "store"),
				}
			},
			message: "no such file or directory",
		},
		{
			name: "a metrics address the kernel refuses",
			make: func(t *testing.T) *config {
				dir := t.TempDir()
				return &config{
					endpoint: "unix://" + filepath.Join(dir, "csi.sock"),
					store:    filepath.Join(dir, "store"),
					metrics:  "127.0.0.1:-1",
				}
			},
			message: "invalid port",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := newServer(t.Context(), c.make(t), quietLogger())
			if err == nil {
				t.Fatal("newServer answered no error, want one")
			}
			if !strings.Contains(err.Error(), c.message) {
				t.Errorf("newServer answered %q, want it to name %q", err, c.message)
			}
		})
	}
}

func TestServeReportsASocketThatWentAway(t *testing.T) {
	failed := errors.New("the socket went away")
	serving := &server{
		serveOn:  func(net.Listener) error { return failed },
		stop:     func() {},
		readings: newMetrics(),
		logger:   quietLogger(),
	}
	if err := serving.serve(t.Context()); !errors.Is(err, failed) {
		t.Errorf("serve answered %v, want %v", err, failed)
	}
}

func TestServeReportsAFailureThatFollowedTheStop(t *testing.T) {
	failed := errors.New("the socket went away")
	released := make(chan struct{})
	serving := &server{
		serveOn:  func(net.Listener) error { <-released; return failed },
		stop:     func() { close(released) },
		readings: newMetrics(),
		logger:   quietLogger(),
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := serving.serve(ctx); !errors.Is(err, failed) {
		t.Errorf("serve answered %v, want %v", err, failed)
	}
}

func TestServeTakesTheStopItAskedForAsNoFailure(t *testing.T) {
	released := make(chan struct{})
	serving := &server{
		serveOn:  func(net.Listener) error { <-released; return grpc.ErrServerStopped },
		stop:     func() { close(released) },
		readings: newMetrics(),
		logger:   quietLogger(),
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := serving.serve(ctx); err != nil {
		t.Errorf("serve answered %v, want no error", err)
	}
}
