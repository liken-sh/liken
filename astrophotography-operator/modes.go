package main

// The modes of the binary that do not run the operator. The first
// argument names one, and the rest are its flags. The solver and the
// worker each serve HTTP on port 8080 in a pod that the operator
// creates, in the worker image, which holds the astrometry programs
// beside this binary.

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

// mode runs one mode until ctx ends, with the arguments after its name,
// and logs to logs.
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
	server, err := solver.Start(options, logs)
	if err != nil {
		return err
	}
	return serveMode(ctx, ":"+strconv.Itoa(solver.Port), server)
}

func runWorker(ctx context.Context, args []string, logs io.Writer) error {
	extractor, err := worker.ParseOptions(args, logs)
	if err != nil {
		return err
	}
	return serveMode(ctx, ":"+strconv.Itoa(worker.Port), worker.NewServer(extractor, logs))
}

// serveMode serves a mode's handler until ctx ends. The kubelet's
// SIGTERM ends ctx, and the server closes at once: a solve or an
// extraction in flight fails, and the operator asks again.
func serveMode(ctx context.Context, address string, handler http.Handler) error {
	listener, err := listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", address, err)
	}
	server := &http.Server{
		Handler: handler,
		// A client that opens a connection and sends no headers holds a
		// goroutine until this bound. The handlers bound their own work.
		ReadHeaderTimeout: 10 * time.Second,
	}
	context.AfterFunc(ctx, func() { _ = server.Close() })
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
