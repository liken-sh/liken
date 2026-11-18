package main

// The operator keeps one INDI client for each INDI server it runs, and
// connects, configures, and reads every device through it. The client
// dials the server's Service only once the server's pod is Ready,
// because the Service has no ready endpoint before then. Each new
// connection reads the whole state again, so a restart of the server or
// the operator needs no other recovery.

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// The wait before dialing again when a connection ends while the
// server's pod is still Ready. No pod event follows such an end, for
// example when kube-proxy has not yet programmed the Service's new
// endpoint, or when the connection broke while the server kept running,
// so a timer must retry. A connection that lasted at least redialReset
// resets the wait to redialFirst.
const (
	redialFirst = time.Second
	redialLimit = 30 * time.Second
	redialReset = time.Minute
)

type indiServer struct {
	name string
	// source is the resource whose server this is, such as
	// Telescope/east. It labels the server's observation series.
	source string
	client *indi.Client
	cancel context.CancelFunc
	done   chan struct{}
	// lock serializes the operator's INDI work on the server, so two
	// runners that share a site server do not interleave their changes
	// to one device.
	lock lock
	// epoch is the number of the server's last structural change, from
	// epochs (locks.go).
	epoch atomic.Uint64
}

type servers struct {
	o      *operator
	mu     sync.Mutex
	byName map[string]*indiServer
}

func newServers(o *operator) *servers {
	return &servers{o: o, byName: map[string]*indiServer{}}
}

// get returns the client of one server, if its pod exists.
func (s *servers) get(name string) (*indiServer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	server, ok := s.byName[name]
	return server, ok
}

// sync opens a client for each server pod, and closes the client of
// each server whose pod is gone.
func (s *servers) sync(ctx context.Context, t *tree) {
	want := map[string]bool{}
	for name, p := range t.pods {
		if p.Metadata.Labels[labelRole] == roleServer {
			want[name] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name := range want {
		if _, open := s.byName[name]; !open {
			s.byName[name] = s.open(ctx, name, sourceOf(t.pods[name]))
		}
	}
	for name, server := range s.byName {
		if !want[name] {
			s.close(server)
		}
	}
}

func (s *servers) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, server := range s.byName {
		s.close(server)
	}
}

// close ends a server's connection and deletes its observation series,
// because the server's pod is gone and nothing is left to observe. The
// caller holds s.mu.
func (s *servers) close(server *indiServer) {
	server.cancel()
	<-server.done
	delete(s.byName, server.name)
	s.o.series.forgetSource(server.source)
}

func (s *servers) open(parent context.Context, name, source string) *indiServer {
	ctx, cancel := context.WithCancel(parent)
	address := serviceHost(name, s.o.namespace) + ":" + strconv.Itoa(serverPort)
	var options []indi.Option
	if s.o.dialer != nil {
		options = append(options, indi.WithDialer(s.o.dialer))
	}
	server := &indiServer{name: name, source: source, client: indi.NewClient(address, options...), lock: newLock(), cancel: cancel, done: make(chan struct{})}
	server.epoch.Store(epochs.Add(1))
	events := server.client.Subscribe(ctx)
	s.o.series.observation(source, false)
	var group sync.WaitGroup
	// Every INDI event wakes the operator, because a runner may be
	// waiting for a property and the status writer reports it. An
	// Updated or Message event cannot change which properties the server
	// defines, so it only notifies o.changed. Any other event can, so it
	// also advances the server's epoch and notifies o.structure.
	group.Go(func() {
		for e := range events {
			s.o.series.observation(source, e.Kind != indi.Disconnected)
			if e.Kind == indi.Updated || e.Kind == indi.Message {
				s.o.changed.notify()
			} else {
				server.epoch.Store(epochs.Add(1))
				s.o.structure.notify()
			}
		}
	})
	group.Go(func() { s.o.keepOpen(ctx, server.name, server.client.Run) })
	go func() {
		group.Wait()
		close(server.done)
	}()
	return server
}

// keepOpen runs a client's connection while the pod of that name is
// Ready, and dials again after the connection ends. A new server opens
// its port shortly after its pod becomes Ready, so the first dial is
// often refused, and the reservation's status already shows that it
// waits for the connection. So keepOpen logs a refusal only when the
// attempt before it was also refused.
func (o *operator) keepOpen(ctx context.Context, name string, run func(context.Context) error) {
	pause := redialFirst
	refused := false
	for {
		err := o.waitFor(ctx, nil, func(t *tree) (bool, string, error) {
			p, ok := t.pods[name]
			return ok && p.ready(), "", nil
		})
		if err != nil {
			return
		}
		began := time.Now()
		err = run(ctx)
		if ctx.Err() != nil {
			return
		}
		wasRefused := refused
		refused = errors.Is(err, syscall.ECONNREFUSED)
		if !refused || wasRefused {
			o.logf("the connection to %s ended: %v", name, err)
		}
		if time.Since(began) >= redialReset {
			pause = redialFirst
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		pause = min(2*pause, redialLimit)
	}
}
