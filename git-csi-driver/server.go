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
}

// newServer makes the store, takes the socket, and registers the
// Identity and Node services. It fails before it listens when the store
// cannot be made, because a driver with no store can hold no volume.
//
// newServer makes the store, takes the socket, and registers the
// Identity and Node services. ctx is the driver's run, and every fetch
// loop the Node service starts ends with it.
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

	registered := grpc.NewServer(grpc.UnaryInterceptor(logCalls(logger)))
	csi.RegisterIdentityServer(registered, &identity{store: cfg.store})
	csi.RegisterNodeServer(registered, newNode(ctx, cfg, newEvents(cfg.nodeID, logger), logger))
	return &server{grpc: registered, listener: listener}, nil
}

// serve blocks until the context ends, then stops the server and lets
// a call in flight finish.
func (s *server) serve(ctx context.Context) error {
	served := make(chan error, 1)
	go func() { served <- s.grpc.Serve(s.listener) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		s.grpc.GracefulStop()
		// A context that is over before Serve reaches the socket makes
		// Serve return ErrServerStopped. That is the stop this run asked
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
