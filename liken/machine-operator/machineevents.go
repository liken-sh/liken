package main

// Readers that wake the reconcile loop when the kernel reports a
// change on the machine.
//
// A pass reads two kinds of machine state that change with no
// Kubernetes event. The device inventory and the CDI specifications of
// claims come from a walk of sysfs (dra.go, cdi.go), and the host
// entries come from the host's /etc/hosts (hosts.go). The kernel sends
// a uevent when a device arrives, leaves, or changes its driver, and an
// inotify event when a file changes, so a reader for each wakes the
// loop.
//
// Sysctls have no such event. A write through /proc/sys sends inotify
// events only to watches on the same procfs mount, because each mount
// has its own inodes. The pod and the host have separate /proc mounts,
// so the loop reads sysctls on a timer instead (backstop.go).
//
// main.go opens the readers before the first pass, so a change during
// the first walk or the first read of a file still wakes the loop.
//
// When a reader stops after it opened, the operator exits. A reader
// stops only on a poll error or a closed descriptor, which the
// operator cannot repair, and a stopped reader would miss every later
// change. The kubelet restarts the container, and the new process opens
// new readers before its first pass, so it reads any change made while
// no reader ran.

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
)

// listenForUevents opens the uevent listener, filtered to the events
// that can change the device inventory (hardware.InventoryEvent), so
// the veth pairs that pods create and remove wake nothing. A test
// replaces it.
var listenForUevents = func(ctx context.Context) (<-chan struct{}, error) {
	return hardware.ListenForUeventsMatching(ctx, hardware.InventoryEvent)
}

// watchHostsFile watches the hosts file by name in the host's /etc,
// which the pod mounts (hosts.go). A test replaces it.
var watchHostsFile = func(ctx context.Context) (<-chan struct{}, error) {
	return machine.WatchName(ctx, filepath.Dir(hostsPath), filepath.Base(hostsPath))
}

// hostsWatchFailure returns the error from opening the hosts watch,
// or nil when the directory does not exist. A pod made from a template
// older than the /host/etc mount has no such directory, so the operator
// runs without the watch and hostEntriesCondition reports the missing
// mount. Any other error, such as EMFILE or ENOSPC from inotify, would
// leave the hosts file unwatched for the life of the process, so main
// exits on it.
func hostsWatchFailure(err error) error {
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

var errReaderStopped = errors.New("the reader stopped")

// settleEvents merges a burst of events into one wake signal: it waits
// for one second with no event, or five seconds at most, the same
// intervals as init's hardware watch. A USB device that enumerates
// sends many uevents within milliseconds, and one pass after the burst
// reads all of them. The hosts watch needs it too, because the pass's
// own write to /etc/hosts sends an event. Without it, a pass and
// another process that keeps rewriting the file would overwrite each
// other as fast as both can write. With it, that costs at most one pass
// every five seconds. Each relay runs in its own goroutine, so a reader
// that stays busy for the full five seconds does not delay the others'
// wakes.
func settleEvents(ctx context.Context, events <-chan struct{}) {
	hardware.Settle(ctx, events, time.Second, 5*time.Second)
}

// relay wakes the loop for each event from a reader, until ctx ends,
// and returns errReaderStopped when the reader closes its channel.
// When settle is not nil, relay calls it after the first event of a
// burst and wakes the loop once the burst ends.
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
