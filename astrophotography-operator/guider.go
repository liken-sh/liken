package main

// observatory-operator runs PHD2 for each Guider of a reservation's
// telescope and connects it to the guide camera and the mount, but never
// calibrates or guides. That is the job of the reservation's
// holder, so the runner drives PHD2 through its event server at
// the Guider's status.endpoint, as KStars does.
//
// The runner keeps one connection open for the whole reservation, and
// the phd2 client updates its state from PHD2's events. So a star lost
// during an exposure is already in the state when the frame ends.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2"
)

// guiderWait is how long a step waits for the connection to PHD2. It
// covers a restart of PHD2's pod, which takes a few seconds, and the
// redials of keepOpen, whose backoff starts at one second.
const guiderWait = 30 * time.Second

type guiderLink struct {
	name   string
	client *phd2.Client

	mu sync.Mutex
	// unsettled is true after a guide or a dither that did not settle.
	// PHD2 can report Guiding while the star is off the lock position,
	// so the next frame starts guiding again first.
	unsettled bool
}

func (l *guiderLink) setUnsettled(unsettled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unsettled = unsettled
}

func (l *guiderLink) isUnsettled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.unsettled
}

func (l *guiderLink) waitOpen(ctx context.Context) error {
	waiting, cancel := context.WithTimeout(ctx, guiderWait)
	defer cancel()
	if err := l.client.WaitFor(waiting, func(s phd2.State) bool { return s.Open }); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("PHD2 of Guider %s does not respond within %s", l.name, guiderWait)
	}
	return nil
}

// phd2Guiding keeps one guiderLink for each runner whose session has a
// Guider.
type phd2Guiding struct {
	mu    sync.Mutex
	links map[*runner]*guiderLink
}

func newPHD2Guiding() *phd2Guiding {
	return &phd2Guiding{links: map[*runner]*guiderLink{}}
}

// open creates the runner's link before its first step, so no step
// finds a session with a Guider and no link. It returns the function
// that keeps the connection open until ctx ends. A session with no
// Guider gets no link, and its frames are not guided.
func (g *phd2Guiding) open(r *runner) func(context.Context) {
	name, ok := sessionGuider(r.res.Status.Session)
	if !ok {
		return func(context.Context) {}
	}
	l := &guiderLink{name: name, client: phd2.NewClient(name,
		phd2.WithDialer(guiderDialer{o: r.o, namespace: r.namespace, name: name}))}
	g.mu.Lock()
	g.links[r] = l
	g.mu.Unlock()
	return func(ctx context.Context) {
		defer func() {
			g.mu.Lock()
			delete(g.links, r)
			g.mu.Unlock()
		}()
		r.keepOpen(ctx, "the connection to PHD2 of Guider "+name, l.client.Run)
	}
}

// link returns a runner's link, or nil when its session has no Guider.
func (g *phd2Guiding) link(r *runner) *guiderLink {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.links[r]
}

// sessionGuider returns the name of the session's Guider, and false
// when the session has none. When there is more than one, it takes the
// first by name, because map iteration order is random.
func sessionGuider(session *observatory.Session) (string, bool) {
	if len(session.Guiders) == 0 {
		return "", false
	}
	var names []string
	for name := range session.Guiders {
		names = append(names, name)
	}
	slices.Sort(names)
	return names[0], true
}

// errNoEndpoint is the error of a dial while the Guider's status has no
// endpoint, for example while its pod starts.
var errNoEndpoint = errors.New("has no status.endpoint")

// guiderDialer reads the Guider's status.endpoint at each dial, so a
// redial after PHD2's pod restarts reaches the new endpoint.
type guiderDialer struct {
	o               *operator
	namespace, name string
}

func (d guiderDialer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	view := d.o.stores.collections[observatory.GuiderKind.Name].View()
	guider, ok := informer.Cached[observatory.Guider](view, d.namespace+"/"+d.name)
	if !ok || guider.Status.Endpoint == nil || guider.Status.Endpoint.Host == "" {
		return nil, fmt.Errorf("Guider %s %w", d.name, errNoEndpoint)
	}
	e := guider.Status.Endpoint
	return d.o.phd2Dialer.DialContext(ctx, network, net.JoinHostPort(e.Host, strconv.Itoa(int(e.Port))))
}
