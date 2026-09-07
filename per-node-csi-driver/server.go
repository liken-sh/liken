package main

// server.go holds the socket the kubelet connects to, the gRPC server
// that answers on it, and the log line every call writes.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// endpointScheme is the only scheme --endpoint accepts. The kubelet
// reaches a CSI plugin through a Unix socket in its plugins directory,
// and nothing else connects to this server.
const endpointScheme = "unix://"

// server is the listening socket and the gRPC server registered on it.
type server struct {
	grpc     *grpc.Server
	listener net.Listener
	// serveOn and stop are the gRPC server's own two calls, held
	// as fields so a test can drive a Serve that fails after the stop.
	serveOn func(net.Listener) error
	stop    func()
	// The gauge and the listener that serves it. An empty --metrics
	// leaves the listener nil.
	readings *metrics
	metrics  net.Listener
	logger   *slog.Logger
}

// newServer takes the socket and registers the Identity and Node
// services on it. It makes the store first. A driver that cannot make
// the store fails before it listens, because it could publish no copy.
// ctx is the driver's run, and the sweep ends with it.
func newServer(ctx context.Context, cfg *config, logger *slog.Logger) (*server, error) {
	socket, found := strings.CutPrefix(cfg.endpoint, endpointScheme)
	if !found {
		return nil, fmt.Errorf("--endpoint %q does not begin with %s", cfg.endpoint, endpointScheme)
	}
	if err := os.MkdirAll(cfg.store, 0o755); err != nil {
		return nil, err
	}

	// A pod that was killed leaves its socket file on the node, and the
	// next pod has to bind the same path. The file is removed, not
	// reported, because no other process ever owns it.
	if err := os.Remove(socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}

	readings := newMetrics()
	posting := newEvents(cfg.nodeID, logger)
	answering := newNode(cfg, posting, readings, logger)
	// The mounts outlive the driver, so a driver that starts takes back
	// the holds the kernel still carries.
	answering.resume(ctx)
	// One watch on PersistentVolumes serves the whole node. The kubelet
	// never tells a node plugin that a volume was deleted, so the watch
	// is how a deletion reaches the copies on this node.
	go newSweeping(answering, posting.client, cfg.sweepEvery, logger).follow(ctx)

	registered := grpc.NewServer(grpc.UnaryInterceptor(logCalls(logger)))
	csi.RegisterIdentityServer(registered, &identity{store: cfg.store})
	csi.RegisterNodeServer(registered, answering)

	metricsListener, err := readings.listen(cfg.metrics)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	return &server{
		grpc:     registered,
		listener: listener,
		serveOn:  registered.Serve,
		stop:     registered.GracefulStop,
		readings: readings,
		metrics:  metricsListener,
		logger:   logger,
	}, nil
}

// serve blocks until the context ends, then stops the server and lets
// a call in flight finish.
func (s *server) serve(ctx context.Context) error {
	served := make(chan error, 1)
	go func() { served <- s.serveOn(s.listener) }()
	if s.metrics != nil {
		go serveMetrics(ctx, s.metrics, s.readings, s.logger)
	}
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		s.stop()
		// A context that is over before Serve reaches the socket makes
		// serve return ErrServerStopped. That is the stop this run asked
		// for, not a failure.
		if err := <-served; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return err
		}
		return nil
	}
}

// logCalls writes one line per RPC with its name and its status code.
// The kubelet's calls are the driver's whole input, and a person who
// reads the log has to see them.
func logCalls(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		call *grpc.UnaryServerInfo,
		handle grpc.UnaryHandler,
	) (any, error) {
		answer, err := handle(ctx, request)
		logger.InfoContext(ctx, "call",
			"rpc", call.FullMethod,
			"code", status.Code(err).String())
		return answer, err
	}
}
