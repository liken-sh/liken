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
	name string
	// source names the PHD2 in the observation series: its Guider, such
	// as Guider/east.
	source string
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
			g.byName[name] = g.open(ctx, name, sourceOf(t.pods[name]))
		}
	}
	for name, c := range g.byName {
		if !want[name] {
			g.close(c)
		}
	}
}

func (g *guiderConns) stopAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.byName {
		g.close(c)
	}
}

// close ends a connection, and deletes its observation series, as
// servers.close does. The caller holds g.mu.
func (g *guiderConns) close(c *guiderConn) {
	c.cancel()
	<-c.done
	delete(g.byName, c.name)
	g.o.series.forgetSource(c.source)
}

func (g *guiderConns) open(parent context.Context, name, source string) *guiderConn {
	ctx, cancel := context.WithCancel(parent)
	c := &guiderConn{name: name, source: source, cancel: cancel, done: make(chan struct{})}
	g.o.series.observation(source, false)
	options := []phd2.Option{phd2.WithNotify(func(s phd2.State) {
		// Each change is a message on an open connection, except the
		// end of the connection.
		g.o.series.observation(source, s.Open)
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
