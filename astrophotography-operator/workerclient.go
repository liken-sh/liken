package main

// The runner's client of the reservation's worker (plan 02), the pod
// that mounts the outbox claim beside the camera. The runner asks it
// two questions: the free space of the outbox before each exposure,
// and, after a restart, whether the frame that status.exposure names
// is in the outbox. Both are the one GET of worker.PathOutbox.

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

// workerTimeout bounds one request to the worker. The worker answers
// from one statfs and one walk of the outbox's target directories.
const workerTimeout = 30 * time.Second

// workerURL answers the URL of a path of a reservation's worker: its
// Service, <Reservation>-astrophotography, in the reservation's
// namespace.
func workerURL(namespace, reservation, path string) *url.URL {
	return &url.URL{Scheme: "http", Host: reservation + "-astrophotography." + namespace + ".svc:" + strconv.Itoa(worker.Port), Path: path}
}

// outboxOf asks a reservation's worker for the state of its outbox.
func (o *operator) outboxOf(ctx context.Context, namespace, reservation string) (worker.OutboxAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, workerTimeout)
	defer cancel()
	var answer worker.OutboxAnswer
	request := (&http.Request{Method: http.MethodGet, URL: workerURL(namespace, reservation, worker.PathOutbox), Header: http.Header{}}).WithContext(ctx)
	response, err := o.workerClient.Do(request)
	if err != nil {
		return answer, fmt.Errorf("asking the worker of %s for its outbox: %w", reservation, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		line, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return answer, fmt.Errorf("the worker of %s answered %s: %s", reservation, response.Status, line)
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return answer, fmt.Errorf("reading the worker's answer: %w", err)
	}
	return answer, nil
}
