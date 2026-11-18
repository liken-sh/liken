package main

// A reservation's worker is the pod that mounts the outbox claim on the
// camera's node. The runner reads the outbox from it: the free space
// before each exposure, and whether a frame is in the outbox when the
// driver did not report saving it or when the runner resumes after a
// restart.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/worker"
)

// workerTimeout limits one GET of the outbox. The worker responds to it after
// one statfs call and one walk of the targets' directories.
const workerTimeout = 30 * time.Second

// workerURL returns the URL of a path on a reservation's worker, through
// the worker's Service <reservation>-astrophotography.
func workerURL(namespace, reservation, path string) *url.URL {
	return &url.URL{Scheme: "http", Host: reservation + "-astrophotography." + namespace + ".svc:" + strconv.Itoa(worker.Port), Path: path}
}

// outboxOf returns the free space and the frames of a reservation's
// outbox.
func (o *operator) outboxOf(ctx context.Context, namespace, reservation string) (worker.OutboxResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, workerTimeout)
	defer cancel()
	var outbox worker.OutboxResponse
	request := (&http.Request{Method: http.MethodGet, URL: workerURL(namespace, reservation, worker.PathOutbox), Header: http.Header{}}).WithContext(ctx)
	response, err := o.peerClient.Do(request)
	if err != nil {
		return outbox, fmt.Errorf("requesting the outbox of the worker of %s: %w", reservation, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		line, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return outbox, fmt.Errorf("the worker of %s responded %s: %s", reservation, response.Status, line)
	}
	if err := json.NewDecoder(response.Body).Decode(&outbox); err != nil {
		return outbox, fmt.Errorf("decoding the outbox response of the worker of %s: %w", reservation, err)
	}
	return outbox, nil
}
