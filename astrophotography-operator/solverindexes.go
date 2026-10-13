package main

// The index list of each solver pod. The solver's pod watches the
// directory of its index files and changes its list when a file
// arrives or leaves (solver/indexwatch.go). So the operator holds one
// request open to each ready pod, GET /indexes?watch=true, which
// answers the list at once and again after each change, and each list
// wakes the Solver loop. When the request ends, the operator opens it
// again, and the first line of the new request gives the whole list.
//
// The request goes to the pod's own address, not the Service: the
// list belongs to one pod, and the Service routes to a new pod only
// after the endpoints controller adds it, some time after the pod turns
// Ready. Until then a request through the Service is refused, or times
// out.

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

// indexFollow is what the operator knows of one pod's list.
type indexFollow struct {
	// indexes is the last list the pod answered, and nil until the
	// first one.
	indexes solver.Indexes
	// failure is the error of the last request, while the pod has
	// answered no list.
	failure error
	stop    context.CancelFunc
}

// indexes answers the last list of a ready pod, and starts to follow
// the pod's list on the first call for it. Before the first list
// arrives, it answers nil, or the error of the request that failed.
func (loop *solverLoop) indexes(ctx context.Context, o *operator, p *podObject) (solver.Indexes, error) {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	follow, held := loop.follows[p.Metadata.UID]
	if !held {
		following, stop := context.WithCancel(ctx)
		follow = &indexFollow{stop: stop}
		loop.follows[p.Metadata.UID] = follow
		url := "http://" + net.JoinHostPort(p.Status.PodIP, strconv.Itoa(solver.Port)) + solver.PathIndexes + "?watch=true"
		loop.running.Go(func() { loop.follow(following, o, follow, p.Metadata.Name, url) })
	}
	return follow.indexes, follow.failure
}

// longEnough is the life of a request after which its end is no
// failure. A request that ends sooner waits retryAfter before the
// next, so a pod that closes each request at once is not asked again
// and again.
const longEnough = time.Second

// follow reads one pod's list until ctx ends.
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
		fmt.Fprintf(o.logs, "%s: following the index list of pod %s: %v\n", managedBy, pod, err)
		if loop.record(follow, nil, fmt.Errorf("reading the index list of pod %s: %w", pod, err)) {
			o.changed.notify()
		}
		if time.Since(opened) >= longEnough {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryAfter):
		}
	}
}

// record keeps a pod's new list, or the failure of a request while the
// pod has answered no list. A pod that answered a list keeps it through
// a failed request, because the list changes only with the pod's
// directory, and the next request answers it again. It answers whether
// the failure changed what the loop reports.
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

// forget stops following each pod that no Solver reports any more.
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

// followIndexes sends one request that follows a pod's list, and hands
// each list to each. It answers why the request ended.
func (o *operator) followIndexes(ctx context.Context, url string, each func(solver.Indexes)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := o.workerClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("GET %s: %s: %s", url, response.Status, strings.TrimSpace(string(text)))
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
