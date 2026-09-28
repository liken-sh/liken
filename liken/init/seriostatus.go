package main

// What the serio walk reports for each declared entry.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/machine"
)

// entryStatuses reports one declared entry: one status for each
// serial line it matches, or one Missing status when it matches none.
// taken keeps a line that another entry took from a second holder, and
// a Missing entry whose lines another entry took says so. It also
// returns the holders it started, numbered by their places in its
// report, for the walk to wait on.
func (r *serioRegistry) entryStatuses(entry machine.SerioAttachment, lines []serialLineInfo, taken map[string]bool) ([]machine.SerioStatus, []startedHolder) {
	base := machine.SerioStatus{Protocol: entry.Protocol, USB: entry.USB}
	p, ok := machine.LookupSerioProtocol(entry.Protocol)
	if !ok {
		base.State = machine.SerioRefused
		base.Message = fmt.Sprintf("this release has no protocol %q; the protocols are %v", entry.Protocol, machine.SerioProtocolNames())
		return []machine.SerioStatus{base}, nil
	}
	var statuses []machine.SerioStatus
	var started []startedHolder
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
		status, h := r.lineStatus(s, p, line)
		if h != nil {
			started = append(started, startedHolder{index: len(statuses), protocol: p, line: line, holder: h})
		}
		statuses = append(statuses, status)
	}
	if len(statuses) == 0 {
		base.State = machine.SerioMissing
		base.Message = missingMessage(entry, p)
		if heldElsewhere {
			base.Message = fmt.Sprintf("every serial line of USB device %s:%s is held for another spec.serio entry",
				entry.USB.Vendor, entry.USB.Product)
		}
		return []machine.SerioStatus{base}, nil
	}
	return statuses, started
}

// serioBindGrace is how long a port may stay unbound before the status
// blames its driver. pulse8_connect exchanges several commands with the
// adapter before the driver binds, each with a timeout of its own, so
// an unbound port is the ordinary state of a probe for a moment.
const serioBindGrace = 5 * time.Second

// serioUnbound is a port a walk saw with no driver: the tty it is on,
// the port, and when a walk first saw it unbound.
type serioUnbound struct {
	identity uint64
	port     string
	since    time.Time
}

// lineStatus reports one matched line. It starts the line's holder
// when the line has none and nothing stands in the way, and returns
// that holder for the walk to wait on outside the lock.
func (r *serioRegistry) lineStatus(s machine.SerioStatus, p machine.SerioProtocol, line serialLineInfo) (machine.SerioStatus, *serioHolder) {
	if h, held := r.holders[line.tty]; held {
		if h.identity == line.identity {
			return r.settledStatus(s, p, line, h), nil
		}
		// The tty registered again while this holder held the old one,
		// so the holder describes a tty that is gone. Its goroutine
		// ends when the kernel hangs up the old tty, and the registry
		// records the failure now, so an adapter that enumerates again
		// on every attach still counts toward the backoff.
		delete(r.holders, line.tty)
		r.refuse(h, "the tty registered again while the machine held it", r.now().Sub(h.started))
	}
	failures := 0
	refusal, refused := r.refusals[line.usbPath]
	if refused {
		failures = refusal.failures
		// A new tty on the port attaches at once after one failure, and
		// waits for the backoff after more (serioretry.go).
		waits := refusal.identity == line.identity || failures > 1
		if waits && r.now().Before(refusal.retryAt) {
			s.State, s.Message = machine.SerioRefused, refusal.message
			return s, nil
		}
	}
	// The modules are checked before the open, so a machine that is
	// missing one never touches the line. The load that adds a module
	// sends a uevent, and the walk after it tries again. A refusal
	// whose backoff ran out waits one more backoff, so the walk does
	// not come due again at once.
	if missing := missingSerioModules(p); len(missing) > 0 {
		if refused {
			refusal.retryAt = r.now().Add(serioBackoff(failures))
			r.refusals[line.usbPath] = refusal
		}
		s.State = machine.SerioRefused
		s.Message = "declare " + strings.Join(missing, " and ") + " in spec.modules"
		return s, nil
	}
	delete(r.refusals, line.usbPath)
	h := startSerioHolder(r.open, filepath.Join(devRoot, line.tty), p, r.nudge)
	h.identity, h.usbPath, h.started, h.failures = line.identity, line.usbPath, r.now(), failures
	r.holders[line.tty] = h
	s.State = machine.SerioRefused
	s.Message = fmt.Sprintf("the attach calls did not return within %s", serioSettleTimeout)
	return s, h
}

// settledStatus reports a line from its holder, without waiting. A
// holder whose calls have not returned reports the wait. A holder whose
// attach failed leaves the registry, and its port is refused. A holder
// that ended reports its end, and the next walk's prune records it.
func (r *serioRegistry) settledStatus(s machine.SerioStatus, p machine.SerioProtocol, line serialLineInfo, h *serioHolder) machine.SerioStatus {
	select {
	case <-h.settled:
	default:
		s.State = machine.SerioRefused
		s.Message = fmt.Sprintf("the attach calls did not return within %s", serioSettleTimeout)
		return s
	}
	if h.attachErr != nil {
		if r.holders[line.tty] == h {
			delete(r.holders, line.tty)
			r.refuse(h, h.attachErr.Error(), 0)
		}
		s.State, s.Message = machine.SerioRefused, h.attachErr.Error()
		return s
	}
	select {
	case <-h.done:
		s.State, s.Message = machine.SerioRefused, endMessage(h.endErr)
		return s
	default:
	}
	port, nodes, bound := serioPort(line.dir)
	s.Port, s.Nodes, s.Message = port, nodes, ""
	switch {
	case port == "":
		// The holder is in its read, and the kernel registers the port
		// a moment later. The registration's uevents bring the next
		// walk.
		delete(r.unbound, line.tty)
		s.State = machine.SerioRefused
		s.Message = fmt.Sprintf("attaching: the kernel has not registered a serio port on %s yet", line.tty)
	case !bound:
		// A port that stays unbound past the grace is a probe that
		// failed, for example when the adapter did not answer the
		// driver's first commands, and it has no CEC device.
		u, seen := r.unbound[line.tty]
		if !seen || u.identity != line.identity || u.port != port {
			u = serioUnbound{identity: line.identity, port: port, since: r.now()}
			r.unbound[line.tty] = u
		}
		s.State = machine.SerioRefused
		s.Message = fmt.Sprintf("attaching: waiting for %s to bind %s", p.Driver, port)
		if r.now().Sub(u.since) >= serioBindGrace {
			s.Message = fmt.Sprintf("%s on %s has no driver: %s did not bind it; the kernel log names the cause",
				port, line.tty, p.Driver)
		}
	default:
		delete(r.unbound, line.tty)
		s.State = machine.SerioAttached
	}
	return s
}

// pruneUnbound forgets the unbound ports of ttys that left or
// registered again.
func (r *serioRegistry) pruneUnbound(lines []serialLineInfo) {
	for tty, u := range r.unbound {
		if !slices.ContainsFunc(lines, func(l serialLineInfo) bool { return l.tty == tty && l.identity == u.identity }) {
			delete(r.unbound, tty)
		}
	}
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
// the device nodes its driver created under the port, and whether a
// driver is bound to the port.
func serioPort(ttyDir string) (string, []string, bool) {
	entries, err := os.ReadDir(ttyDir)
	if err != nil {
		return "", nil, false
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "serio") {
			continue
		}
		dir := filepath.Join(ttyDir, entry.Name())
		var nodes []string
		for _, node := range hardware.SubtreeNodes(dir) {
			nodes = append(nodes, node.Path)
		}
		_, err := os.Lstat(filepath.Join(dir, "driver"))
		return entry.Name(), nodes, err == nil
	}
	return "", nil, false
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
