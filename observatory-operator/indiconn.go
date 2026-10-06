package main

// The operator holds one INDI client for each INDI server it runs, and
// connects, configures, and reads every device through it. The client
// dials the server's Service, and opens its connection when the
// server's pod is Ready, which is when the Service has a ready
// endpoint. Each new connection reads the whole state again (the indi
// package), so a restart of the server or of the operator costs one
// baseline and nothing else.

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// The pause after a connection that ended while the server's pod stayed
// Ready. It is a clock: no pod event follows such an end, as when
// kube-proxy has not yet programmed the Service's new endpoint, or when
// the connection broke while the server ran. It doubles to its limit,
// and a connection that ran for the reset time starts it again.
const (
	redialFirst = time.Second
	redialLimit = 30 * time.Second
	redialReset = time.Minute
)

type indiServer struct {
	name   string
	client *indi.Client
	cancel context.CancelFunc
	done   chan struct{}
	// lock serializes the operator's INDI work on the server, so two
	// runners that share a site server do not interleave their changes
	// to one device.
	lock lock
}

type servers struct {
	o      *operator
	mu     sync.Mutex
	byName map[string]*indiServer
}

func newServers(o *operator) *servers {
	return &servers{o: o, byName: map[string]*indiServer{}}
}

// get answers the client of one server, when its pod exists.
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
			s.byName[name] = s.open(ctx, name)
		}
	}
	for name, server := range s.byName {
		if !want[name] {
			server.cancel()
			<-server.done
			delete(s.byName, name)
		}
	}
}

func (s *servers) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, server := range s.byName {
		server.cancel()
		<-server.done
		delete(s.byName, name)
	}
}

func (s *servers) open(parent context.Context, name string) *indiServer {
	ctx, cancel := context.WithCancel(parent)
	address := serviceHost(name, s.o.namespace) + ":" + strconv.Itoa(serverPort)
	var options []indi.Option
	if s.o.dialer != nil {
		options = append(options, indi.WithDialer(s.o.dialer))
	}
	server := &indiServer{name: name, client: indi.NewClient(address, options...), lock: newLock(), cancel: cancel, done: make(chan struct{})}
	events := server.client.Subscribe(ctx)
	var group sync.WaitGroup
	// Every INDI event wakes the operator, as a watch event does: a
	// runner may wait for a property, and the status writer shows it.
	// An update or a message changes no device that a server defines,
	// so it rings changed alone (operator.go).
	group.Go(func() {
		for e := range events {
			if e.Kind == indi.Updated || e.Kind == indi.Message {
				s.o.changed.notify()
			} else {
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
// Ready, and opens it again after it ends: an INDI server's connection,
// or a guider's. A new server opens its port a moment after its pod is
// Ready, so the first dial to it is often refused. That refusal is not
// a fault, and the reservation's status already says that it waits for
// the connection, so keepOpen logs only a refusal that follows another
// refusal. A connection that ran makes the next refusal the first
// again, because the server that ended it may have restarted.
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
