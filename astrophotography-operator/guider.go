package main

// A runner's connection to its telescope's PHD2. observatory-operator
// runs PHD2 for each Guider of a reservation's telescope, connects it
// to the guide camera and the mount, and leaves it idle, because
// calibration and guiding belong to the holder (observatory-operator
// plan 09). The Guider's status.endpoint is PHD2's event server, and
// the runner drives PHD2 there with the phd2 client, as KStars does.
//
// The runner keeps one connection open for the whole reservation, so
// the client's state follows PHD2's events: a star lost during an
// exposure is in the state when the frame ends, with no request.

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

// guiderWait bounds the wait for the connection to PHD2 before a step
// that needs it. PHD2's pod starts again in a few seconds after a
// delete (plan 09's drill measured 7 seconds), and the client dials
// again after a backoff of one second.
const guiderWait = 30 * time.Second

// guiderLink is one runner's connection to its Guider's PHD2.
type guiderLink struct {
	// name is the Guider's name.
	name   string
	client *phd2.Client

	mu sync.Mutex
	// unsettled is true after a guide or a dither that did not settle.
	// PHD2 can report Guiding then, with the star off the lock
	// position, so the next frame guides again first.
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

// waitOpen waits until the connection to PHD2 is open, for at most
// guiderWait.
func (l *guiderLink) waitOpen(ctx context.Context) error {
	waiting, cancel := context.WithTimeout(ctx, guiderWait)
	defer cancel()
	if err := l.client.WaitFor(waiting, func(s phd2.State) bool { return s.Open }); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("PHD2 of Guider %s does not answer within %s", l.name, guiderWait)
	}
	return nil
}

// phd2Guiding guides each runner's frames with the PHD2 of its
// session's Guider. It holds the connection of each runner whose
// session has a Guider.
type phd2Guiding struct {
	mu    sync.Mutex
	links map[*runner]*guiderLink
}

func newPHD2Guiding() *phd2Guiding {
	return &phd2Guiding{links: map[*runner]*guiderLink{}}
}

// open makes the runner's link before its first step, so no step finds
// a session with a Guider and no link. A session with no Guider gets no
// link, and its frames are unguided.
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

// link answers a runner's link, and nil when its session has no Guider.
func (g *phd2Guiding) link(r *runner) *guiderLink {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.links[r]
}

// sessionGuider answers the Guider of a session, the first by name,
// and false when the session has none. A telescope has one Guider in
// every example, and the first by name keeps the choice stable when it
// has more.
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

// errNoEndpoint is the error of a dial while the Guider's status names
// no event server, such as while its pod starts.
var errNoEndpoint = errors.New("has no status.endpoint")

// guiderDialer dials the event server that the Guider's status names
// at the moment of each dial, so a connection that ends follows the
// Guider to a new endpoint.
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
