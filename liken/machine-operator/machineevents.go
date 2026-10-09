package main

// The kernel's events for the state on the machine that a pass reads.
//
// A pass reads two kinds of state from the machine itself that the
// kernel announces, beside the facts that init publishes. The device
// inventory and the claims' CDI specifications come from a walk of
// sysfs (dra.go, cdi.go), and the host entries come from the host's
// /etc/hosts (hosts.go). The kernel announces a change to either one:
// a uevent for a device that arrives, leaves, or changes its driver,
// and an inotify event for a file. So two readers turn those events
// into wakes of the loop.
//
// The sysctls have no such event. A write through /proc/sys does send
// inotify events, but only to a watch on the same mount of procfs,
// because each mount has inodes of its own. Each container mounts its
// own /proc, and the host has its own, so a watch in this pod sees
// this pod's writes alone. The ticker's pass reads them (main.go).
//
// The readers open before the first pass, so a change during the first
// walk or the first read of a file still sends a wake after it.
//
// A reader that stops after it opened ends the operator. A reader stops
// only on a poll error or a descriptor that is no longer open, which
// nothing in the operator can repair, and a reader that stopped misses
// every change after it. The kubelet starts the container again with
// its backoff, and the new process opens new readers before its first
// pass, so a change made while nothing listened is still read. This is
// the crash-only rule at the head of main.go.

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
)

// listenForUevents opens the uevent listener, filtered to the events
// that can change what the inventory reads (hardware.InventoryEvent).
// The veth pairs that pods add and remove wake nothing. It is a
// variable so a test can hand the loop a listener whose wakes the test
// sends.
var listenForUevents = func(ctx context.Context) (<-chan struct{}, error) {
	return hardware.ListenForUeventsMatching(ctx, hardware.InventoryEvent)
}

// watchHostsFile watches the name hosts in the host's /etc, the
// directory the pod mounts (hosts.go). It is a variable so a test can
// hand the loop a watch whose wakes the test sends.
var watchHostsFile = func(ctx context.Context) (<-chan struct{}, error) {
	return machine.WatchName(ctx, filepath.Dir(hostsPath), filepath.Base(hostsPath))
}

// errReaderStopped is what relay answers when its reader closes the
// channel.
var errReaderStopped = errors.New("the reader stopped")

// settleEvents gathers a burst of events into one wake, with init's
// intervals for its own hardware watch: one second of quiet, and five
// seconds at most. One USB device that enumerates sends a dozen events
// in a few milliseconds, and the pass after the burst reads every one
// of them. The hosts file needs it too. The pass's own write to
// /etc/hosts sends an event, and a process that keeps writing another
// file would otherwise trade writes with the pass as fast as both can
// write. With the settle, that costs a pass every five seconds at most.
// The settle runs in the relay's goroutine, not in the loop, so a
// stream that holds it at the ceiling never delays a wake from a watch.
func settleEvents(ctx context.Context, events <-chan struct{}) {
	hardware.Settle(ctx, events, time.Second, 5*time.Second)
}

// relay turns a reader's events into wakes of the loop until the
// context ends, and answers errReaderStopped when the reader closes its
// channel. settle, when it is not nil, waits out a burst after the
// first event of it.
func relay(ctx context.Context, events <-chan struct{}, wakes chan<- struct{}, settle func(context.Context, <-chan struct{})) error {
	wake := watch.Signal(wakes)
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-events:
			if !ok {
				return errReaderStopped
			}
			if settle != nil {
				settle(ctx, events)
			}
			wake()
		}
	}
}

// localReads keeps the pass's two reads of the machine itself across
// passes: the walk of sysfs that publishes the inventory and refreshes
// the claims, and the reconcile of /etc/hosts. Each one has an event
// for every change, so a pass that only the ticker woke reuses the last
// result and reads nothing. A read whose last attempt failed runs on
// every pass until it succeeds, because the retry timer that answers
// the failure can arrive after a tick, and the tick's pass must not
// clear the failure it never retried.
type localReads struct {
	// tickOnly is true for a pass that the ticker alone woke. The loop
	// sets it before each pass.
	tickOnly bool

	hostsCurrent bool
	hosts        []machine.HostEntry

	inventoryCurrent bool
}

// hostEntries answers the host entries, from apply unless the pass
// may reuse the last ones.
func (l *localReads) hostEntries(apply func() ([]machine.HostEntry, error)) ([]machine.HostEntry, error) {
	if l != nil && l.tickOnly && l.hostsCurrent {
		return l.hosts, nil
	}
	hosts, err := apply()
	if l != nil {
		l.hosts, l.hostsCurrent = hosts, err == nil
	}
	return hosts, err
}

// walk answers whether the pass walks sysfs.
func (l *localReads) walk() bool {
	return l == nil || !l.tickOnly || !l.inventoryCurrent
}

// walked records whether the walk's slice write and claim refresh all
// succeeded.
func (l *localReads) walked(ok bool) {
	if l != nil {
		l.inventoryCurrent = ok
	}
}
