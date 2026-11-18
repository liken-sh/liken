package main

// The binary runs as the solver or the worker when its first argument
// is the mode's name, and the rest are the mode's flags. The operator
// runs both modes in pods from the worker image, which has the
// astrometry programs.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/astrophotography-operator/worker"
)

// mode runs one mode until ctx ends. args are the arguments after the
// mode's name, and the mode writes its log to logs.
type mode func(ctx context.Context, args []string, logs io.Writer) error

var modes = map[string]mode{
	"solver": runSolver,
	"worker": runWorker,
}

func runSolver(ctx context.Context, args []string, logs io.Writer) error {
	options, err := solver.ParseOptions(args, logs)
	if err != nil {
		return err
	}
	serving, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	server, err := solver.Start(serving, options, logs)
	if err != nil {
		return err
	}
	// When the watch of the index directory fails for good, the mode
	// ends, so that the kubelet restarts the container and the watch
	// starts again.
	go func() {
		select {
		case err := <-server.Failed():
			stop(fmt.Errorf("watching the index files: %w", err))
		case <-serving.Done():
		}
	}()
	if err := serveMode(serving, ":"+strconv.Itoa(solver.Port), server); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	return context.Cause(serving)
}

func runWorker(ctx context.Context, args []string, logs io.Writer) error {
	extractor, err := worker.ParseOptions(args, logs)
	if err != nil {
		return err
	}
	return serveMode(ctx, ":"+strconv.Itoa(worker.Port), worker.NewServer(extractor, logs))
}

// serveMode serves a mode's handler until ctx ends, which the kubelet's
// SIGTERM does. The server then closes its connections at once, so a
// solve or an extraction in flight fails and the operator requests it
// again.
func serveMode(ctx context.Context, address string, handler http.Handler) error {
	listener, err := listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", address, err)
	}
	server := &http.Server{
		Handler: handler,
		// Without this timeout, a client that connects and sends no
		// request headers keeps a goroutine forever. The handlers limit
		// their own work.
		ReadHeaderTimeout: 10 * time.Second,
	}
	context.AfterFunc(ctx, func() { _ = server.Close() })
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
