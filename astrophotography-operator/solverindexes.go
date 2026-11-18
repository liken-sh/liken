package main

// Each solver pod watches the directory of its index files and keeps a
// list of them (solver/indexwatch.go). The operator keeps one request,
// GET /indexes?watch=true, open to each ready pod. The pod sends the
// whole list when the request starts and again after each change, and
// each list wakes the Solver loop. When the request ends, the operator
// opens a new one, and its first list replaces the old one.
//
// The request goes to the pod's IP address, not to the Service, because
// the list is about one pod. The Service also routes to a new pod only
// after the endpoints controller adds it, some time after the pod
// becomes Ready, and until then a request through the Service fails.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/solver"
)

// indexFollow is the state of the request that reads one pod's list.
type indexFollow struct {
	// indexes is the last list the pod sent. It is nil until the first
	// list arrives.
	indexes solver.Indexes
	// failure is the error of the last request. It is set only while the
	// pod has sent no list.
	failure error
	stop    context.CancelFunc
}

// indexes returns the last list that a ready pod sent. The first call
// for a pod starts the request. Until the first list arrives, it
// returns nil, or the error of the last request.
func (loop *solverLoop) indexes(ctx context.Context, o *operator, p *podObject) (solver.Indexes, error) {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	follow, exists := loop.follows[p.Metadata.UID]
	if !exists {
		following, stop := context.WithCancel(ctx)
		follow = &indexFollow{stop: stop}
		loop.follows[p.Metadata.UID] = follow
		url := "http://" + net.JoinHostPort(p.Status.PodIP, strconv.Itoa(solver.Port)) + solver.PathIndexes + "?watch=true"
		loop.running.Go(func() { loop.follow(following, o, follow, p.Metadata.Name, url) })
	}
	return follow.indexes, follow.failure
}

// minRequestLife is how long a request must have been open for the
// loop to open the next one at once. A request that ends sooner is
// followed by a wait of retryAfter, so a pod that closes every request
// immediately does not receive a stream of new requests.
const minRequestLife = time.Second

// follow reads one pod's lists until ctx ends, and opens a new request
// each time the previous one ends.
func (loop *solverLoop) follow(ctx context.Context, o *operator, follow *indexFollow, pod, url string) {
	for {
		opened := time.Now()
		err := o.followIndexes(ctx, url, func(indexes solver.Indexes) {
			loop.record(follow, indexes, nil)
			o.changed.notify()
		})
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(o.logs, "%s: the request for the index list of pod %s ended: %v\n", managedBy, pod, err)
		if loop.record(follow, nil, fmt.Errorf("reading the index list of pod %s: %w", pod, err)) {
			o.changed.notify()
		}
		if time.Since(opened) >= minRequestLife {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryAfter):
		}
	}
}

// record stores a pod's new list, or the failure of a request while the
// pod has sent no list. It returns whether the stored state changed. A
// failed request does not clear a list, because the list changes only
// when the pod's directory changes, and the next request sends it
// again.
func (loop *solverLoop) record(follow *indexFollow, indexes solver.Indexes, failure error) bool {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	switch {
	case indexes != nil:
		follow.indexes, follow.failure = indexes, nil
		return true
	case follow.indexes == nil:
		follow.failure = failure
		return true
	}
	return false
}

// forget stops following each pod that no Solver reports.
func (loop *solverLoop) forget(current map[string]bool) {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	for uid, follow := range loop.follows {
		if !current[uid] {
			follow.stop()
			delete(loop.follows, uid)
		}
	}
}

// wait returns when every goroutine that follows a pod has stopped.
func (loop *solverLoop) wait() { loop.running.Wait() }

// followIndexes sends one watch request to a pod and calls each with
// every list the pod sends. It returns the error that ended the
// request.
func (o *operator) followIndexes(ctx context.Context, url string, each func(solver.Indexes)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := o.peerClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("GET %s returned %s: %s", url, response.Status, strings.TrimSpace(string(text)))
	}
	lines := json.NewDecoder(response.Body)
	for {
		var indexes solver.Indexes
		if err := lines.Decode(&indexes); err != nil {
			return err
		}
		each(indexes)
	}
}
