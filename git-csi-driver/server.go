package main

// server.go creates the Unix socket the kubelet connects to, serves the
// CSI gRPC services on it, and logs and times each call.

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

// endpointScheme is the only scheme --endpoint accepts, because the
// kubelet connects to a CSI plugin over a Unix socket.
const endpointScheme = "unix://"

type server struct {
	grpc     *grpc.Server
	listener net.Listener
	// serveOn and stop are fields so that a test can replace them.
	serveOn func(net.Listener) error
	stop    func()
	// metrics is nil when --metrics is empty.
	metrics         *metrics
	metricsListener net.Listener
	// hooks is nil on the node plugin.
	hooks  *webhook
	logger *slog.Logger
}

// newServer creates the store directory and returns an error before it
// listens when it cannot. The controller registers the Identity,
// Controller, and Node services, and the node plugin registers Identity
// and Node. Every goroutine the node plugin starts ends when ctx ends.
func newServer(ctx context.Context, cfg *config, logger *slog.Logger) (*server, error) {
	socket, found := strings.CutPrefix(cfg.endpoint, endpointScheme)
	if !found {
		return nil, fmt.Errorf("--endpoint %q does not begin with %s", cfg.endpoint, endpointScheme)
	}
	if err := os.MkdirAll(cfg.store, 0o755); err != nil {
		return nil, err
	}

	// A killed pod leaves its socket file on the node, and the next pod
	// binds the same path. Only this driver uses the path, so removing
	// the old file is safe.
	if err := os.Remove(socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}

	registry := newMetrics()
	registry.reportBuildInfo(version)
	var hooks *webhook
	var webhookListener net.Listener
	registered := grpc.NewServer(grpc.ChainUnaryInterceptor(logCalls(logger), recordCalls(registry)))
	csi.RegisterIdentityServer(registered,
		&identity{store: cfg.store, controller: cfg.controller})
	if cfg.controller {
		csi.RegisterControllerServer(registered, &controller{})
		// The resizer sidecar reads node capabilities from the
		// controller's socket and exits when the Node service is
		// missing, so the controller serves one that declares no
		// capabilities.
		csi.RegisterNodeServer(registered, controllerNode{})
		// The webhook receives push notifications from a forge, and
		// requests a pull by annotating the volume's PersistentVolume.
		hooks, err = newWebhook(cfg, registry, logger)
		if err != nil {
			closeAll(listener)
			return nil, err
		}
		webhookListener = hooks.listener
	} else {
		plugin := newNode(ctx, cfg, newEvents(ctx, cfg.nodeID, logger), registry, logger)
		// Mounts survive a driver restart, so the new driver resumes
		// each volume the store still records.
		plugin.resume(ctx)
		registry.registerNodeFacts(plugin.volumesByRepo)
		// Measuring the store at start makes git_csi_store_bytes show
		// the resumed store instead of zero until the first sweep. It
		// runs in a goroutine so a large store does not delay the socket.
		go plugin.measureStore(ctx)
		// A pull request from outside the node arrives as an annotation
		// on a PersistentVolume.
		go plugin.demands.run(ctx)
		go plugin.sweepPeriodically(ctx)
		csi.RegisterNodeServer(registered, plugin)
	}

	metricsListener, err := registry.listen(cfg.metrics)
	if err != nil {
		closeAll(listener, webhookListener)
		return nil, err
	}
	return &server{
		grpc:            registered,
		listener:        listener,
		serveOn:         registered.Serve,
		stop:            registered.GracefulStop,
		metrics:         registry,
		metricsListener: metricsListener,
		hooks:           hooks,
		logger:          logger,
	}, nil
}

// closeAll skips a nil listener, which is one that was not configured.
func closeAll(listeners ...net.Listener) {
	for _, listener := range listeners {
		if listener != nil {
			_ = listener.Close()
		}
	}
}

// serve stops the server gracefully when ctx ends, so calls in flight
// finish.
func (s *server) serve(ctx context.Context) error {
	served := make(chan error, 1)
	go func() { served <- s.serveOn(s.listener) }()
	if s.metricsListener != nil {
		go serveMetrics(ctx, s.metricsListener, s.metrics, s.logger)
	}
	if s.hooks != nil {
		go s.hooks.serve(ctx)
	}
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		s.stop()
		// When ctx ends before Serve starts, Serve returns
		// ErrServerStopped. That is the requested stop, not a failure.
		if err := <-served; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return err
		}
		return nil
	}
}

// logCalls logs each RPC with its method name and status code, except a
// successful call whose handler called quietCall.
func logCalls(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		call *grpc.UnaryServerInfo,
		handle grpc.UnaryHandler,
	) (any, error) {
		note := &callNote{}
		response, err := handle(context.WithValue(ctx, callNoteKey{}, note), request)
		if err == nil && note.quiet {
			return response, err
		}
		logger.InfoContext(ctx, "call",
			"rpc", call.FullMethod,
			"code", status.Code(err).String())
		return response, err
	}
}

// callNote passes the quiet flag from a handler to logCalls. The
// handler returns before logCalls reads the flag, on the same goroutine,
// so it needs no lock.
type callNote struct {
	quiet bool
}

type callNoteKey struct{}

// quietCall marks a call that changed nothing, so logCalls does not
// log it. It does nothing when ctx has no callNote, as when a test calls
// the handler directly.
func quietCall(ctx context.Context) {
	if note, found := ctx.Value(callNoteKey{}).(*callNote); found {
		note.quiet = true
	}
}

// recordCalls records every RPC on git_csi_reconcile_duration_seconds
// and each failed one on git_csi_reconcile_errors_total, labeled with the
// CSI method name. A quiet call is recorded too, because the metrics
// treat the CSI calls as the driver's reconcile loop.
func recordCalls(registry *metrics) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		call *grpc.UnaryServerInfo,
		handle grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		response, err := handle(ctx, request)
		registry.observeCall(operationName(call.FullMethod), time.Since(start), err != nil)
		return response, err
	}
}

// operationName returns the last element of a gRPC method path, such
// as NodePublishVolume.
func operationName(fullMethod string) string {
	if i := strings.LastIndexByte(fullMethod, '/'); i >= 0 {
		return fullMethod[i+1:]
	}
	return fullMethod
}
