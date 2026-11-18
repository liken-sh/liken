package main

// The operator keeps one connection to the PHD2 event server of each
// guider pod, as indiconn.go keeps one INDI client for each INDI
// server. keepOpen dials when the pod is Ready, and dials again when
// the connection ends. The status writer reports the client's state,
// and the guider steps send set_connected and stop_capture through it.

import (
	"context"
	"strconv"
	"sync"

	"github.com/liken-sh/liken/observatory-operator/phd2"
)

type guiderConn struct {
	name string
	// source is the label of the guider's observation series, such as
	// Guider/east.
	source string
	client *phd2.Client
	cancel context.CancelFunc
	done   chan struct{}
	// last is whether the connection was open and the equipment
	// connected at the previous notification.
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

func (g *guiderConns) get(name string) (*guiderConn, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.byName[name]
	return c, ok
}

// sync opens a connection for each guider pod in the tree, and closes
// the connection of each guider whose pod is gone.
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

// close stops a connection and deletes its observation series. The
// caller holds g.mu.
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
		g.o.series.observation(source, s.Open)
		// StartGuider and a Ready runner wait on structure for the
		// connection and the equipment, so only a change to either
		// notifies structure, which also notifies changed. A guide step
		// notifies changed alone, so it does not wake them.
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
