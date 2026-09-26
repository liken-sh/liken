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
// uevents, and the walk after them starts a new holder.
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
// through the restart. The holders do not stop at shutdown either. They write
// to no disk, so the quiesce has nothing to wait for, and the reboot
// system call ends them. The port stays in place for as long as any
// pod runs.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
// calls. The open does not wait for the modem lines, and the ioctls
// return at once, so the calls take microseconds. The bound exists so
// that a driver that blocks cannot stall the walk, and with it every
// other attachment.
var serioSettleTimeout = 5 * time.Second

// serioRegistry is the state that outlives the component: the
// declared entries, the holders, and what the last walk reported.
// Only the walk and the declaration touch the maps, both under mu. A
// holder never touches the registry. It reports through its own
// channels (serioattach.go).
type serioRegistry struct {
	mu       sync.Mutex
	declared []machine.SerioAttachment
	holders  map[string]*serioHolder
	// refusals holds the message of a refusal that a walk does not
	// retry, keyed by tty: a call the kernel refused, or a read that
	// ended while the tty stayed. Opening the line again on every
	// uevent would toggle the adapter's modem lines and get the same
	// refusal again. A refusal clears when its tty leaves, because the
	// next plug is new hardware, and when the declared list changes.
	refusals map[string]string
	nudge    chan struct{}
	open     openLine

	// printed is the report the console last showed, and published is
	// the report the facts tree last took. They differ only after a
	// failed write, which the next walk retries. Only the component's
	// goroutine touches them, and the plane never runs two of it.
	printed, published []machine.SerioStatus
}

func newSerioRegistry(open openLine) *serioRegistry {
	return &serioRegistry{
		holders:  map[string]*serioHolder{},
		refusals: map[string]string{},
		nudge:    make(chan struct{}, 1),
		open:     open,
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
	r.refusals = map[string]string{}
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
			select {
			case <-ctx.Done():
				return nil
			case <-r.nudge:
			case <-uevents:
				settle(ctx, uevents, serioQuiet, 5*time.Second)
			}
			if ctx.Err() != nil {
				return nil
			}
		}
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
// returns the report.
func (r *serioRegistry) walk() []machine.SerioStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.declared) == 0 && len(r.holders) == 0 {
		return nil
	}
	lines := discoverSerialLines()
	r.prune(lines)
	// The most specific entry wins. Entries that name a serial take
	// their lines first, so an entry without one, declared earlier,
	// cannot hold a line that a specific entry names. The report keeps
	// the declaration order.
	taken := map[string]bool{}
	byEntry := make([][]machine.SerioStatus, len(r.declared))
	for _, specific := range []bool{true, false} {
		for i, entry := range r.declared {
			if (entry.USB.Serial != "") == specific {
				byEntry[i] = r.entryStatuses(entry, lines, taken)
			}
		}
	}
	return slices.Concat(byEntry...)
}

// prune removes the holders that ended, and forgets the refusals of
// the ttys that left. A holder whose read ended while its tty is still
// here records a refusal, because the port went away for a reason
// other than an unplug, and a new holder at once could repeat the
// same end on every uevent the attach itself sends.
func (r *serioRegistry) prune(lines []serialLineInfo) {
	present := func(tty string) bool {
		return slices.ContainsFunc(lines, func(l serialLineInfo) bool { return l.tty == tty })
	}
	for tty, h := range r.holders {
		select {
		case <-h.done:
		default:
			continue
		}
		delete(r.holders, tty)
		if h.attachErr == nil && present(tty) {
			r.refusals[tty] = endMessage(h.endErr)
		}
	}
	for tty := range r.refusals {
		if !present(tty) {
			delete(r.refusals, tty)
		}
	}
}

// endMessage words a holder's end for the status.
func endMessage(err error) string {
	if err != nil {
		return "read: " + err.Error()
	}
	return "read: the kernel ended the attachment while the tty stayed"
}

// entryStatuses reports one declared entry: one status for each
// serial line it matches, or one Missing status when it matches none.
// taken keeps a line that another entry took from a second holder, and
// a Missing entry whose lines another entry took says so.
func (r *serioRegistry) entryStatuses(entry machine.SerioAttachment, lines []serialLineInfo, taken map[string]bool) []machine.SerioStatus {
	base := machine.SerioStatus{Protocol: entry.Protocol, USB: entry.USB}
	p, ok := machine.LookupSerioProtocol(entry.Protocol)
	if !ok {
		base.State = machine.SerioRefused
		base.Message = fmt.Sprintf("this release has no protocol %q; the protocols are %v", entry.Protocol, machine.SerioProtocolNames())
		return []machine.SerioStatus{base}
	}
	var statuses []machine.SerioStatus
	heldElsewhere := false
	for _, line := range lines {
		if !entry.Matches(line.vendor, line.product, line.serial) {
			continue
		}
		if taken[line.tty] {
			heldElsewhere = true
			continue
		}
		taken[line.tty] = true
		s := base
		s.TTY = line.tty
		statuses = append(statuses, r.lineStatus(s, p, line))
	}
	if len(statuses) == 0 {
		base.State = machine.SerioMissing
		base.Message = missingMessage(entry, p)
		if heldElsewhere {
			base.Message = fmt.Sprintf("every serial line of USB device %s:%s is held for another spec.serio entry",
				entry.USB.Vendor, entry.USB.Product)
		}
		return []machine.SerioStatus{base}
	}
	return statuses
}

// lineStatus reports one matched line, and starts its holder when it
// has none and nothing stands in the way.
func (r *serioRegistry) lineStatus(s machine.SerioStatus, p machine.SerioProtocol, line serialLineInfo) machine.SerioStatus {
	h, held := r.holders[line.tty]
	if !held {
		if message, refused := r.refusals[line.tty]; refused {
			s.State, s.Message = machine.SerioRefused, message
			return s
		}
		// The modules are checked before the open, so a machine that
		// is missing one never touches the line. The load that adds a
		// module sends a uevent, and the walk after it tries again.
		if missing := missingSerioModules(p); len(missing) > 0 {
			s.State = machine.SerioRefused
			s.Message = "declare " + strings.Join(missing, " and ") + " in spec.modules"
			return s
		}
		h = startSerioHolder(r.open, filepath.Join(devRoot, line.tty), p, r.nudge)
		r.holders[line.tty] = h
	}
	select {
	case <-h.settled:
	case <-time.After(serioSettleTimeout):
		s.State = machine.SerioRefused
		s.Message = fmt.Sprintf("the attach calls did not return within %s", serioSettleTimeout)
		return s
	}
	if h.attachErr != nil {
		delete(r.holders, line.tty)
		r.refusals[line.tty] = h.attachErr.Error()
		s.State, s.Message = machine.SerioRefused, h.attachErr.Error()
		return s
	}
	s.State = machine.SerioAttached
	s.Port, s.Nodes = serioPort(line.dir)
	return s
}

// missingSerioModules names the modules an attach needs that the
// kernel does not hold: serport, and the protocol's driver. The line
// driver is not checked here, because a line that exists proves it.
func missingSerioModules(p machine.SerioProtocol) []string {
	var missing []string
	for _, name := range []string{p.Discipline, p.Driver} {
		if !moduleIsResident(sysModuleDir, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// missingMessage says why an entry matches no line. A USB device with
// the entry's identity and no tty is an adapter whose line driver is
// not loaded, and the fix is a module. No such device is an adapter
// that is unplugged.
func missingMessage(entry machine.SerioAttachment, p machine.SerioProtocol) string {
	identity := entry.USB.Vendor + ":" + entry.USB.Product
	if entry.USB.Serial != "" {
		identity += " with serial " + entry.USB.Serial
	}
	if usbDevicePresent(entry) {
		return fmt.Sprintf("USB device %s has no serial line; declare %s in spec.modules", identity, p.LineDriver)
	}
	return fmt.Sprintf("no USB device %s is plugged in", identity)
}

// serioPort reads the serio port that serport registered under a tty,
// and the device nodes its driver created under the port. The port is
// absent for the moment between the holder entering its read and the
// kernel registering the port, and the uevents of the registration
// bring the next walk.
func serioPort(ttyDir string) (string, []string) {
	entries, err := os.ReadDir(ttyDir)
	if err != nil {
		return "", nil
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "serio") {
			continue
		}
		var nodes []string
		for _, node := range hardware.SubtreeNodes(filepath.Join(ttyDir, entry.Name())) {
			nodes = append(nodes, node.Path)
		}
		return entry.Name(), nodes
	}
	return "", nil
}

// describeSerio renders one status as a console line.
func describeSerio(s machine.SerioStatus) string {
	entry := s.Attachment().String()
	switch s.State {
	case machine.SerioAttached:
		line := fmt.Sprintf("liken: serio: %s attached on %s", entry, s.TTY)
		if s.Port != "" {
			line += " as " + s.Port
		}
		if len(s.Nodes) > 0 {
			line += " (" + strings.Join(s.Nodes, ", ") + ")"
		}
		return line
	case machine.SerioMissing:
		return fmt.Sprintf("liken: serio: %s is missing: %s", entry, s.Message)
	}
	if s.TTY == "" {
		return fmt.Sprintf("liken: serio: %s refused: %s", entry, s.Message)
	}
	return fmt.Sprintf("liken: serio: %s on %s refused: %s", entry, s.TTY, s.Message)
}

func serioStatusEqual(a, b machine.SerioStatus) bool {
	return a.Protocol == b.Protocol && a.USB == b.USB && a.TTY == b.TTY && a.Port == b.Port &&
		a.State == b.State && a.Message == b.Message && slices.Equal(a.Nodes, b.Nodes)
}
