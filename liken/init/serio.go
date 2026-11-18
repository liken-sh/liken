package main

// The serio watch is the machine-plane component that attaches each
// serial-line adapter that spec.serio declares, so the kernel creates
// its CEC and input devices. machine/serio.go defines an attachment,
// and serioattach.go defines the holder that keeps one attached.
//
// Each walk lists the ttys under /sys/class/tty, reads the USB
// identity of the device behind each one, and compares it with the
// declared entries. It starts a holder for each matching tty that has
// none.
//
// An unplug needs no separate handling. The kernel hangs up the tty,
// serport's read ends, the holder ends, and the next walk no longer
// lists the tty. The uevents of the next plug start a walk, which
// starts a new holder. serioretry.go retries a refused attachment.
//
// The attachment runs in init, like module loading, because no pod can
// do it. The TIOCSETD ioctl that selects serport needs CAP_SYS_ADMIN.
// The DRA driver of the machine operator also hands a container only
// the device nodes that exist when the container starts, so a pod that
// attached the line would never receive the nodes that the attachment
// creates.
//
// The registry is a package variable, so the holders keep reading
// across a restart of the component. Holders do not stop at shutdown.
// They write to no disk, so the reboot system call ends them, and the
// attachment stays in place while any pod runs.
import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/machine"
)

// serioQuiet is how long the uevents must stay quiet before a walk
// runs. Plugging in an adapter sends uevents for the USB device, its
// interfaces, and the tty, and attaching it sends uevents for the
// port, the CEC adapter, and the input device. A pod that needs the
// adapter waits for the walk, so the time is short.
const serioQuiet = 250 * time.Millisecond

// serioSettleTimeout is the longest a walk waits for a new holder to
// finish its attach calls. Each control transfer of cdc_acm times out
// after 5 seconds, and TIOCSETD waits up to 5 seconds for the line
// discipline's lock, so the timeout is above the sum of those waits.
// The walk holds no lock while it waits, so the hardware watch and the
// module loader do not wait for it.
const serioSettleTimeout = 15 * time.Second

// serioRegistry holds the declared entries, the holders, the refusals
// that walks record, and the ports that a walk saw without a driver.
// mu guards the maps, and only walks and declare use them. A holder
// does not lock the registry. It reports through its own channels
// (serioattach.go). A send on nudge wakes the watch. declare sends
// when it changes the list, and a holder sends when it ends.
type serioRegistry struct {
	mu       sync.Mutex
	declared []machine.SerioAttachment
	holders  map[string]*serioHolder
	refusals map[string]serioRefusal
	unbound  map[string]serioUnbound
	nudge    chan struct{}
	open     openLine
	now      func() time.Time

	// printed is the last report that was printed to the console, and
	// published is the last report that was written to the facts tree.
	// They differ after a failed write, which the next walk tries
	// again. Only the component's goroutine uses them, because the
	// machine plane never runs two copies of a component.
	printed, published []machine.SerioStatus
}

func newSerioRegistry(open openLine) *serioRegistry {
	return &serioRegistry{
		holders:  map[string]*serioHolder{},
		refusals: map[string]serioRefusal{},
		unbound:  map[string]serioUnbound{},
		nudge:    make(chan struct{}, 1),
		open:     open,
		now:      time.Now,
	}
}

// serioAttachments is the registry that init uses. It is a package
// variable so that the boot, the module loader, and the hardware watch
// share it.
var serioAttachments = newSerioRegistry(openTTY)

// declare replaces the declared list, clears the recorded refusals,
// and wakes the watch. The boot declares the list from the manifest.
// The module loader declares the list from a live load.
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

// declaredEntries returns a copy of the declared list. The hardware
// watch uses it to decide which serial-line adapters are still
// unclaimed.
func (r *serioRegistry) declaredEntries() []machine.SerioAttachment {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.declared)
}

// watchSerio returns the component. It walks as soon as it starts, so
// an adapter that is already plugged in attaches without a uevent.
func watchSerio(r *serioRegistry, tree machine.FactsTree) func(context.Context) error {
	return func(ctx context.Context) error {
		uevents, err := listenForUevents(ctx)
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
			listening := waitForSerioWork(ctx, uevents, r.nudge, retry)
			if timer != nil {
				timer.Stop()
			}
			if ctx.Err() != nil {
				return nil
			}
			if !listening {
				return errUeventsStopped
			}
		}
	}
}

// waitForSerioWork returns when a walk is due: after a burst of
// uevents settles, after a nudge and the uevents that follow it
// settle, or when retry fires. It returns false when the uevent
// listener stopped.
//
// It waits for uevents to settle after a nudge, because a holder ends
// when the kernel hangs up its tty, and the kernel removes the tty
// after that. A walk in between would still list the tty and report a
// refusal that the next walk then clears.
func waitForSerioWork(ctx context.Context, uevents, nudge <-chan struct{}, retry <-chan time.Time) bool {
	select {
	case <-ctx.Done():
	case <-nudge:
		hardware.Settle(ctx, uevents, serioQuiet, 5*time.Second)
	case _, ok := <-uevents:
		if !ok {
			return false
		}
		hardware.Settle(ctx, uevents, serioQuiet, 5*time.Second)
	case <-retry:
	}
	return true
}

// publish runs one walk, prints each status that is new, and writes
// serio/ in the facts tree when the report changed.
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

// walk compares the declared entries with the serial lines in sysfs,
// starts a holder for each matching line that has none, and returns
// the report. It waits for the new holders without holding the
// registry's lock, then locks again to record how they finished.
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

// startedHolder is a holder that a walk started, and the index of its
// entry in the report.
type startedHolder struct {
	index    int
	protocol machine.SerioProtocol
	line     serialLineInfo
	holder   *serioHolder
}

// plan is the part of a walk that holds the lock. It reads the serial
// lines, removes ended holders, builds the report for every entry, and
// starts a holder for each line that needs one.
func (r *serioRegistry) plan() ([]machine.SerioStatus, []startedHolder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.declared) == 0 && len(r.holders) == 0 {
		return nil, nil
	}
	lines := discoverSerialLines()
	r.prune()
	r.pruneUnbound(lines)
	// Entries with a serial choose their lines first. Otherwise an
	// entry without a serial, declared earlier, could take a line that
	// a more specific entry matches. The report keeps declaration
	// order.
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
	// Each entry numbered its holders within its own statuses, so add
	// the statuses of the entries before it to get the index in the
	// whole report.
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
