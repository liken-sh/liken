package main

// The serio watch: the machine-plane component that keeps every
// spec.serio attachment in place (machine/serio.go says what an
// attachment is, and serioattach.go how one is held).
//
// The watch has the shape of the disk links watch. It walks sysfs once
// at start, and again after every settled burst of uevents. Each walk
// lists the ttys under /sys/class/tty, reads the USB identity above
// each one, and compares it with the declared entries. A matched tty
// with no holder gets one. An unplug needs no handling of its own: the
// kernel hangs up the tty, serport ends the read, the holder ends, and
// the next walk no longer lists the tty. The plug that follows sends
// uevents, and the walk after them starts a new holder. A refusal is
// kept for the tty it happened on and retried after a bounded backoff
// (serioretry.go), so an adapter that resets comes back by itself.
//
// The attachment belongs on the machine plane for the reason module
// loading does. It is what makes the hardware appear, and no pod can
// do it: the machine operator's DRA driver delivers only the nodes
// that exist when a container starts, so a pod that attached the line
// would never receive the nodes it created, and TIOCSETD to serport
// needs CAP_SYS_ADMIN.
//
// The holders live in a registry that outlives the component, the same
// as the reaper's. A restart of the component starts a new uevent
// listener and a new walk, and the holders stay in their reads
// through the restart. The holders do not stop at shutdown either.
// They write to no disk, so the quiesce has nothing to wait for, and
// the reboot system call ends them. The port stays in place for as
// long as any pod runs.

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/machine"
)

// serioQuiet is how long a walk waits for a burst of uevents to stop.
// An adapter's plug announces the USB device, its interfaces, and the
// tty, and an attach announces the port, the CEC adapter, and the
// input device. A pod holding the adapter waits for the result, so the
// wait is short, like the disk links watch's.
const serioQuiet = 250 * time.Millisecond

// serioSettleTimeout bounds the wait for a new holder's four attach
// calls. On a healthy adapter they take microseconds, but the kernel
// lets them wait longer: cdc_acm's control transfers each time out
// after 5 seconds, and TIOCSETD waits up to 5 seconds for the line
// discipline's lock. The bound sits above the sum of those waits, so
// a slow adapter settles inside it. The walk holds no lock while it
// waits, so the hardware watch and the module loader never wait
// behind it.
var serioSettleTimeout = 15 * time.Second

// serioRegistry is the state that outlives the component: the
// declared entries, the holders, and what the last walk reported.
// Only the walk and the declaration touch the maps, both under mu. A
// holder never touches the registry. It reports through its own
// channels (serioattach.go).
type serioRegistry struct {
	mu       sync.Mutex
	declared []machine.SerioAttachment
	holders  map[string]*serioHolder
	refusals map[string]serioRefusal
	nudge    chan struct{}
	open     openLine
	now      func() time.Time

	// printed is the report the console last showed, and published is
	// the report the facts tree last took. They differ only after a
	// failed write, which the next walk retries. Only the component's
	// goroutine touches them, and the plane never runs two of it.
	printed, published []machine.SerioStatus
}

func newSerioRegistry(open openLine) *serioRegistry {
	return &serioRegistry{
		holders:  map[string]*serioHolder{},
		refusals: map[string]serioRefusal{},
		nudge:    make(chan struct{}, 1),
		open:     open,
		now:      time.Now,
	}
}

// serioAttachments is the boot's one registry, package-level for the
// same reason the machine plane is.
var serioAttachments = newSerioRegistry(openTTY)

// declare replaces the declared list and wakes the walk. The boot
// declares the manifest's list, and the module loader declares the
// list a live load applied.
func (r *serioRegistry) declare(entries []machine.SerioAttachment) {
	r.mu.Lock()
	r.declared = slices.Clone(entries)
	r.refusals = map[string]serioRefusal{}
	r.mu.Unlock()
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

// declaredEntries returns a copy of the declared list, for the
// hardware watch's unclaimed report.
func (r *serioRegistry) declaredEntries() []machine.SerioAttachment {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.declared)
}

// watchSerio is the component. It publishes the first walk at once,
// so an adapter that is plugged in at boot attaches without waiting
// for a uevent.
func watchSerio(r *serioRegistry, tree machine.FactsTree) func(context.Context) error {
	return func(ctx context.Context) error {
		uevents, err := hardware.ListenForUevents(ctx)
		if err != nil {
			return err
		}
		for {
			r.publish(tree)
			var retry <-chan time.Time
			var timer *time.Timer
			if wait, ok := r.nextRetry(); ok {
				timer = time.NewTimer(wait)
				retry = timer.C
			}
			waitForSerioWork(ctx, uevents, r.nudge, retry)
			if timer != nil {
				timer.Stop()
			}
			if ctx.Err() != nil {
				return nil
			}
		}
	}
}

// waitForSerioWork returns when the next walk is due: after a burst of
// uevents settles, after a holder's nudge and the uevents that follow
// it settle, or when a refusal's backoff runs out. A holder ends when
// the kernel hangs up its tty, which comes before the kernel removes
// the tty, so a walk at the nudge itself would read a tty that is
// about to leave and report a refusal for one walk.
func waitForSerioWork(ctx context.Context, uevents, nudge <-chan struct{}, retry <-chan time.Time) {
	select {
	case <-ctx.Done():
	case <-nudge:
		settle(ctx, uevents, serioQuiet, 5*time.Second)
	case <-uevents:
		settle(ctx, uevents, serioQuiet, 5*time.Second)
	case <-retry:
	}
}

// publish runs one walk, prints each status that changed, and
// rewrites serio/ in the facts tree when the report changed.
func (r *serioRegistry) publish(tree machine.FactsTree) {
	statuses := r.walk()
	for _, s := range statuses {
		if !slices.ContainsFunc(r.printed, func(p machine.SerioStatus) bool { return serioStatusEqual(p, s) }) {
			fmt.Println(describeSerio(s))
		}
	}
	r.printed = statuses
	if slices.EqualFunc(statuses, r.published, serioStatusEqual) {
		return
	}
	if tree.WriteSerio(statuses) == nil {
		r.published = statuses
	}
}

// walk compares the declared entries with the serial lines sysfs
// shows now, starts a holder for each matched line that has none, and
// returns the report. It waits for the holders it started without the
// registry's lock, and takes the lock again to record their outcomes.
func (r *serioRegistry) walk() []machine.SerioStatus {
	statuses, started := r.plan()
	if len(started) == 0 {
		return statuses
	}
	deadline := time.NewTimer(serioSettleTimeout)
	defer deadline.Stop()
	for _, s := range started {
		select {
		case <-s.holder.settled:
		case <-deadline.C:
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range started {
		statuses[s.index] = r.settledStatus(statuses[s.index], s.protocol, s.line, s.holder)
	}
	return statuses
}

// startedHolder is a holder a walk started, and the place in the
// report its outcome fills.
type startedHolder struct {
	index    int
	protocol machine.SerioProtocol
	line     serialLineInfo
	holder   *serioHolder
}

// plan is the part of a walk that runs under the lock: it reads the
// lines, prunes the holders, and reports every entry, starting a
// holder where one is due.
func (r *serioRegistry) plan() ([]machine.SerioStatus, []startedHolder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.declared) == 0 && len(r.holders) == 0 {
		return nil, nil
	}
	lines := discoverSerialLines()
	r.prune(lines)
	// The most specific entry wins. Entries that name a serial take
	// their lines first, so an entry without one, declared earlier,
	// cannot hold a line that a specific entry names. The report keeps
	// the declaration order.
	taken := map[string]bool{}
	byEntry := make([][]machine.SerioStatus, len(r.declared))
	started := make([][]startedHolder, len(r.declared))
	for _, specific := range []bool{true, false} {
		for i, entry := range r.declared {
			if (entry.USB.Serial != "") == specific {
				byEntry[i], started[i] = r.entryStatuses(entry, lines, taken)
			}
		}
	}
	// Each entry numbered its started holders within its own report,
	// so the offsets move them to their places in the whole report.
	var all []startedHolder
	offset := 0
	for i := range byEntry {
		for _, s := range started[i] {
			s.index += offset
			all = append(all, s)
		}
		offset += len(byEntry[i])
	}
	return slices.Concat(byEntry...), all
}
