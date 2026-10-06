package main

// The operator holds one connection to the event server of each
// guider's PHD2, the way it holds one INDI client for each server
// (indiconn.go). The connection opens when the guider's pod is Ready,
// and the client reads PHD2's state from it: the baseline when it
// opens, and the events after that. The status writer reports that
// state, and the guider's steps send set_connected and stop_capture
// through it.

import (
	"context"
	"strconv"
	"sync"

	"github.com/liken-sh/liken/observatory-operator/phd2"
)

type guiderConn struct {
	name   string
	client *phd2.Client
	cancel context.CancelFunc
	done   chan struct{}
	// last is what structure last rang for: whether the connection was
	// open and the equipment connected.
	last [2]bool
}

type guiderConns struct {
	o      *operator
	mu     sync.Mutex
	byName map[string]*guiderConn
}

func newGuiderConns(o *operator) *guiderConns {
	return &guiderConns{o: o, byName: map[string]*guiderConn{}}
}

// get answers the connection of one guider's pod, by the pod's name.
func (g *guiderConns) get(name string) (*guiderConn, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.byName[name]
	return c, ok
}

// sync opens a connection for each guider pod, and closes the
// connection of each guider whose pod is gone.
func (g *guiderConns) sync(ctx context.Context, t *tree) {
	want := map[string]bool{}
	for name, p := range t.pods {
		if p.Metadata.Labels[labelRole] == roleGuider {
			want[name] = true
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for name := range want {
		if _, open := g.byName[name]; !open {
			g.byName[name] = g.open(ctx, name)
		}
	}
	for name, c := range g.byName {
		if !want[name] {
			c.cancel()
			<-c.done
			delete(g.byName, name)
		}
	}
}

func (g *guiderConns) stopAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name, c := range g.byName {
		c.cancel()
		<-c.done
		delete(g.byName, name)
	}
}

func (g *guiderConns) open(parent context.Context, name string) *guiderConn {
	ctx, cancel := context.WithCancel(parent)
	c := &guiderConn{name: name, cancel: cancel, done: make(chan struct{})}
	options := []phd2.Option{phd2.WithNotify(func(s phd2.State) {
		// A guide step changes the status and nothing a runner waits
		// for, so it rings changed alone. The connection and the
		// equipment are what StartGuider and a Ready runner wait for,
		// so a change to either rings structure too (operator.go).
		now := [2]bool{s.Open, s.Equipment != nil && *s.Equipment}
		if now != c.last {
			c.last = now
			g.o.structure.notify()
			return
		}
		g.o.changed.notify()
	})}
	if g.o.dialer != nil {
		options = append(options, phd2.WithDialer(g.o.dialer))
	}
	c.client = phd2.NewClient(serviceHost(name, g.o.namespace)+":"+strconv.Itoa(phd2.Port), options...)
	go func() {
		defer close(c.done)
		g.o.keepOpen(ctx, name, c.client.Run)
	}()
	return c
}
