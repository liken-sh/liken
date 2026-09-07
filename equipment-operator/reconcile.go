package main

// The operator's loop: level-triggered, woken by a watch, with a ticker
// as the backstop. A pass reads the whole collection, so a lost event
// costs at most one tick and a restarted operator starts correct.
// One client per Receiver holds the receiver's connection for as long
// as the Receiver stands. A debounced writer folds the burst of lines
// that follows one change into a single status write.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// How often the loop reconciles with nothing to prompt it.
const backstopInterval = 30 * time.Second

// How long a burst of lines is collected before one status write.
var statusDebounce = 250 * time.Millisecond

// receiverUnit is one Receiver's running parts: the connection, the
// status writer, and the session that holds the level.
type receiverUnit struct {
	name       string
	address    string
	client     *Client
	busAddress string
	now        func() time.Time
	denon      *denonClient
	cancel     context.CancelFunc
	dirty      chan struct{}
	generation atomic.Int64

	mutex   sync.Mutex
	session *session
	applied ReceiverStatus
	written bool
}

// observe is where every line the receiver sends reaches the operator.
// It wakes the status writer, and it reaches the session that owns the
// level.
func (u *receiverUnit) observe(event denonEvent) {
	poke(u.dirty)
	u.mutex.Lock()
	held := u.session
	u.mutex.Unlock()
	if held != nil {
		held.observe(event)
	}
}

// report writes the status a burst of lines settles on, one write per
// burst, and only when the write would change something.
func (u *receiverUnit) report(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-u.dirty:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(statusDebounce):
		}
		drainPokes(u.dirty)
		// A select answers a ready timer as readily as a ready context, so a
		// unit stopped inside the debounce is asked again here before it
		// writes.
		if ctx.Err() != nil {
			return
		}
		u.write()
	}
}

func (u *receiverUnit) write() {
	status := buildReceiverStatus(u.denon.State(), u.generation.Load(), u.applied.Conditions, u.now())
	if u.written && sameStatus(status, u.applied) {
		return
	}
	if _, err := ApplyReceiverStatus(u.client, u.name, status); err != nil {
		fmt.Fprintf(os.Stderr, "writing the status of receiver %s: %v\n", u.name, err)
		return
	}
	u.applied, u.written = status, true
}

// setSession starts, replaces, or lifts the session. A session that has
// not changed is left alone, because power and input are one-shots the
// receiver answers once.
func (u *receiverUnit) setSession(ctx context.Context, spec *ReceiverSession) {
	u.mutex.Lock()
	held := u.session
	u.mutex.Unlock()

	if held != nil && spec != nil && held.spec == *spec {
		return
	}
	if held != nil {
		u.mutex.Lock()
		u.session = nil
		u.mutex.Unlock()
		held.stop()
	}
	if spec == nil {
		return
	}
	started := startSession(ctx, u.name, *spec, u.denon, u.busAddress)
	u.mutex.Lock()
	u.session = started
	u.mutex.Unlock()
}

// stop lifts the session and closes the connection, which is what a
// deleted Receiver leaves behind.
func (u *receiverUnit) stop() {
	u.mutex.Lock()
	held := u.session
	u.session = nil
	u.mutex.Unlock()
	if held != nil {
		held.stop()
	}
	u.cancel()
}

// controller holds what every pass needs and the units it runs.
type controller struct {
	client     *Client
	busAddress string
	wake       chan struct{}
	now        func() time.Time
	units      map[string]*receiverUnit
}

func newController(client *Client, busAddress string) *controller {
	return &controller{
		client:     client,
		busAddress: busAddress,
		wake:       make(chan struct{}, 1),
		now:        time.Now,
		units:      map[string]*receiverUnit{},
	}
}

// pass derives every unit from the collection as it stands now. It
// starts a client for a Receiver that names a protocol, moves a session
// that changed, and stops the client of a Receiver that is gone.
func (c *controller) pass(ctx context.Context) error {
	list, err := ListReceivers(c.client)
	if err != nil {
		return err
	}

	live := map[string]bool{}
	for index := range list.Items {
		receiver := &list.Items[index]
		if receiver.Spec.Denon == nil {
			continue
		}
		live[receiver.Metadata.Name] = true
		c.reconcile(ctx, receiver)
	}

	for name, unit := range c.units {
		if !live[name] {
			unit.stop()
			delete(c.units, name)
		}
	}
	return nil
}

// reconcile brings one Receiver's unit up to its spec. An address that
// changed is a different receiver, so the unit is replaced and not
// redialled.
func (c *controller) reconcile(ctx context.Context, receiver *Receiver) {
	name := receiver.Metadata.Name
	unit, held := c.units[name]
	if held && unit.address != receiver.Spec.Denon.Address {
		unit.stop()
		delete(c.units, name)
		held = false
	}
	if !held {
		unit = c.start(ctx, receiver)
		c.units[name] = unit
	}
	unit.generation.Store(receiver.Metadata.Generation)
	unit.setSession(ctx, receiver.Spec.Session)
}

func (c *controller) start(parent context.Context, receiver *Receiver) *receiverUnit {
	ctx, cancel := context.WithCancel(parent)
	unit := &receiverUnit{
		name:       receiver.Metadata.Name,
		address:    receiver.Spec.Denon.Address,
		client:     c.client,
		busAddress: c.busAddress,
		now:        c.now,
		cancel:     cancel,
		dirty:      make(chan struct{}, 1),
	}
	unit.denon = newDenonClient(receiver.Spec.Denon.Address, unit.observe)
	// The generation is stored before anything can write, so the first
	// status names the spec it was built from.
	unit.generation.Store(receiver.Metadata.Generation)
	go unit.denon.Run(ctx)
	go unit.report(ctx)
	// The first write says the operator holds the receiver and has not
	// reached it yet, before any line arrives.
	poke(unit.dirty)
	return unit
}

// run reconciles once before any event arrives, then on every wake and
// every backstop tick, until ctx ends.
func (c *controller) run(ctx context.Context) {
	ticker := time.NewTicker(backstopInterval)
	defer ticker.Stop()
	for {
		if err := c.pass(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "listing receivers: %v\n", err)
		}
		select {
		case <-ctx.Done():
			c.stopAll()
			return
		case <-c.wake:
		case <-ticker.C:
		}
	}
}

func (c *controller) stopAll() {
	for name, unit := range c.units {
		unit.stop()
		delete(c.units, name)
	}
}

// poke never blocks, and a wake channel buffers exactly one, because
// the pass that answers a wake reads the whole collection.
func poke(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// drainPokes clears the wakes a burst queued behind the one already
// taken.
func drainPokes(wake <-chan struct{}) {
	for {
		select {
		case <-wake:
		default:
			return
		}
	}
}

// operate reads the configuration and hands it to serve. Every failure
// here ends the process, because the kubelet restarts the pod with
// backoff and the failure shows in kubectl instead of hiding in a retry
// loop.
func operate() {
	config := readSettings()
	if config.busAddress == "" {
		fmt.Fprintf(os.Stderr, "%s is unset; the Deployment must name the broker\n", busAddressVariable)
		os.Exit(1)
	}

	client, err := InClusterClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster config: %v\n", err)
		os.Exit(1)
	}

	if err := serve(context.Background(), client, config.busAddress); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serve proves the collection can be read, starts the watch from the
// version that first list carried, and runs the loop until ctx ends.
func serve(ctx context.Context, client *Client, busAddress string) error {
	list, err := ListReceivers(client)
	if err != nil {
		return fmt.Errorf("listing receivers: %w", err)
	}

	operator := newController(client, busAddress)
	go watchReceivers(ctx, client, list.Metadata.ResourceVersion, operator.wake)
	operator.run(ctx)
	return nil
}
