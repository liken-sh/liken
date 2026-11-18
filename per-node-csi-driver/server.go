package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// --endpoint accepts only a Unix socket, because that is how the
// kubelet connects to a CSI plugin.
const endpointScheme = "unix://"

type server struct {
	grpc     *grpc.Server
	listener net.Listener
	// serveOn and stop are the gRPC server's Serve and GracefulStop. A
	// test replaces them to make Serve fail.
	serveOn func(net.Listener) error
	stop    func()
	// metrics is nil when --metrics is empty.
	metrics         *metrics
	metricsListener net.Listener
	logger          *slog.Logger
}

// newServer creates the store directory before it listens, so a driver
// that could publish no copy fails before the kubelet can call it. The
// sweep runs until ctx ends.
func newServer(ctx context.Context, cfg *config, logger *slog.Logger) (*server, error) {
	socket, found := strings.CutPrefix(cfg.endpoint, endpointScheme)
	if !found {
		return nil, fmt.Errorf("--endpoint %q does not begin with %s", cfg.endpoint, endpointScheme)
	}
	if err := os.MkdirAll(cfg.store, 0o755); err != nil {
		return nil, fmt.Errorf("creating the store directory: %w", err)
	}

	// A killed driver pod leaves its socket file on the node, and
	// net.Listen fails when the file exists.
	if err := os.Remove(socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("removing the old CSI socket: %w", err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listening on the CSI socket: %w", err)
	}

	metrics := newMetrics()
	events := newEvents(ctx, cfg.nodeID, logger)
	node := newNode(cfg, events, metrics, logger)
	// Bind mounts outlive the driver's process, so a starting driver
	// rebuilds its mount records from the record files and the mount
	// table.
	node.loadRecords(ctx)
	// The kubelet does not tell a node plugin when a PersistentVolume is
	// deleted, so the sweep watches PersistentVolumes and deletes the
	// copies that none names.
	go newSweeper(node, events.client, cfg.sweepEvery, logger).run(ctx)

	registered := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metricsInterceptor(metrics), logCalls(logger)))
	csi.RegisterIdentityServer(registered, &identity{store: cfg.store})
	csi.RegisterNodeServer(registered, node)

	metricsListener, err := metrics.listen(cfg.metrics)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("opening the metrics listener: %w", err)
	}
	return &server{
		grpc:            registered,
		listener:        listener,
		serveOn:         registered.Serve,
		stop:            registered.GracefulStop,
		metrics:         metrics,
		metricsListener: metricsListener,
		logger:          logger,
	}, nil
}

// serve runs the gRPC server until ctx ends, then stops it gracefully,
// so calls in progress finish. It returns Serve's error when Serve
// fails first.
func (s *server) serve(ctx context.Context) error {
	served := make(chan error, 1)
	go func() { served <- s.serveOn(s.listener) }()
	if s.metricsListener != nil {
		go serveMetrics(ctx, s.metricsListener, s.metrics, s.logger)
	}
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		s.stop()
		// Serve returns ErrServerStopped when the stop came before it
		// started.
		if err := <-served; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return err
		}
		return nil
	}
}

func metricsInterceptor(m *metrics) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		call *grpc.UnaryServerInfo,
		handle grpc.UnaryHandler,
	) (any, error) {
		started := time.Now()
		response, err := handle(ctx, request)
		m.recordCall(operationName(call.FullMethod), time.Since(started), err)
		return response, err
	}
}

// operationName turns /csi.v1.Node/NodePublishVolume into
// NodePublishVolume.
func operationName(fullMethod string) string {
	if i := strings.LastIndex(fullMethod, "/"); i != -1 {
		return fullMethod[i+1:]
	}
	return fullMethod
}

func logCalls(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		call *grpc.UnaryServerInfo,
		handle grpc.UnaryHandler,
	) (any, error) {
		response, err := handle(ctx, request)
		logger.InfoContext(ctx, "Handled a CSI call",
			"rpc", call.FullMethod,
			"code", status.Code(err).String())
		return response, err
	}
}
