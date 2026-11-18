package main

// The serio walk reports a status for each declared spec.serio entry.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/machine"
)

// entryStatuses returns one status for each serial line that matches
// entry, or one Missing status when none matches. taken holds the ttys
// that earlier entries of this walk claimed, so two entries never start
// a holder on the same tty. It also returns the holders it started,
// with the index of each one's status, so the walk can wait for them to
// settle.
func (r *serioRegistry) entryStatuses(entry machine.SerioAttachment, lines []serialLineInfo, taken map[string]bool) ([]machine.SerioStatus, []startedHolder) {
	base := machine.SerioStatus{Protocol: entry.Protocol, USB: entry.USB}
	p, ok := machine.LookupSerioProtocol(entry.Protocol)
	if !ok {
		base.State = machine.SerioRefused
		base.Message = fmt.Sprintf("this release has no protocol %q. The protocols are %v", entry.Protocol, machine.SerioProtocolNames())
		return []machine.SerioStatus{base}, nil
	}
	var statuses []machine.SerioStatus
	var started []startedHolder
	claimedByOther := false
	for _, line := range lines {
		if !entry.Matches(line.vendor, line.product, line.serial) {
			continue
		}
		if taken[line.tty] {
			claimedByOther = true
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
		if claimedByOther {
			base.Message = fmt.Sprintf("every serial line of USB device %s:%s is in use by another spec.serio entry",
				entry.USB.Vendor, entry.USB.Product)
		}
		return []machine.SerioStatus{base}, nil
	}
	return statuses, started
}

// serioBindGrace is how long a port may stay unbound before the status
// reports that its driver failed to bind. pulse8_connect sends several
// commands to the adapter, each with its own timeout, before the driver
// binds, so a port is briefly unbound during a normal probe.
const serioBindGrace = 5 * time.Second

// serioUnbound records when a walk first found a serio port with no
// driver bound.
type serioUnbound struct {
	identity uint64
	port     string
	since    time.Time
}

// lineStatus returns the status of one matched line. When the line has
// no holder, it starts one, unless the port's refusal backoff has not
// ended or a module is missing. It returns the new holder so the walk
// can wait for it after it releases the registry's lock.
func (r *serioRegistry) lineStatus(s machine.SerioStatus, p machine.SerioProtocol, line serialLineInfo) (machine.SerioStatus, *serioHolder) {
	if h, held := r.holders[line.tty]; held {
		if h.identity == line.identity {
			return r.settledStatus(s, p, line, h), nil
		}
		// The tty has a new identity, so the kernel registered it again
		// and this holder has the old, removed tty open. Its goroutine
		// ends when the kernel hangs up the old tty. Record the failure
		// now, not when the goroutine ends, so an adapter that
		// enumerates again on every attach still raises the backoff.
		delete(r.holders, line.tty)
		r.refuse(h, "the kernel registered the tty again while init had it open", r.now().Sub(h.started))
	}
	failures := 0
	refusal, refused := r.refusals[line.usbPath]
	if refused {
		failures = refusal.failures
		// The same tty always waits for the backoff. A new tty on the
		// port waits only after more than one failure.
		waits := refusal.identity == line.identity || failures > 1
		if waits && r.now().Before(refusal.retryAt) {
			s.State, s.Message = machine.SerioRefused, refusal.message
			return s, nil
		}
	}
	// Check the modules before opening the line, because opening it
	// toggles the adapter's modem lines. Loading a module sends a uevent
	// that starts another walk. A refusal whose backoff has ended gets a
	// new retryAt, so the next retry is not due at once.
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

// settledStatus returns the status of a line from its holder, without
// waiting for the holder. When the attach failed, it removes the holder
// and records a refusal on its USB port. When the holder has ended, the
// next walk's prune records the failure.
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
		// The kernel registers the port shortly after the holder starts
		// reading, and the uevents of that registration start the next
		// walk.
		delete(r.unbound, line.tty)
		s.State = machine.SerioRefused
		s.Message = fmt.Sprintf("attaching: the kernel has not registered a serio port on %s yet", line.tty)
	case !bound:
		// A port still unbound after serioBindGrace means the driver's
		// probe failed, for example because the adapter did not answer
		// its first commands.
		u, seen := r.unbound[line.tty]
		if !seen || u.identity != line.identity || u.port != port {
			u = serioUnbound{identity: line.identity, port: port, since: r.now()}
			r.unbound[line.tty] = u
		}
		s.State = machine.SerioRefused
		s.Message = fmt.Sprintf("attaching: waiting for %s to bind %s", p.Driver, port)
		if r.now().Sub(u.since) >= serioBindGrace {
			s.Message = fmt.Sprintf("%s on %s has no driver because %s did not bind it. The kernel log has the reason",
				port, line.tty, p.Driver)
		}
	default:
		delete(r.unbound, line.tty)
		s.State = machine.SerioAttached
	}
	return s
}

// pruneUnbound deletes the unbound records of ttys that are gone or
// that registered again with a new identity.
func (r *serioRegistry) pruneUnbound(lines []serialLineInfo) {
	for tty, u := range r.unbound {
		if !slices.ContainsFunc(lines, func(l serialLineInfo) bool { return l.tty == tty && l.identity == u.identity }) {
			delete(r.unbound, tty)
		}
	}
}

// missingSerioModules returns the modules of the attach that the kernel
// has not loaded: the line discipline and the protocol's driver. It
// does not check the line driver, because the tty exists only when that
// driver is loaded.
func missingSerioModules(p machine.SerioProtocol) []string {
	var missing []string
	for _, name := range []string{p.Discipline, p.Driver} {
		if !moduleIsResident(sysModuleDir, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// missingMessage explains why an entry matches no serial line. A USB
// device with the entry's identity and no tty means that its line
// driver is not loaded.
func missingMessage(entry machine.SerioAttachment, p machine.SerioProtocol) string {
	identity := entry.USB.Vendor + ":" + entry.USB.Product
	if entry.USB.Serial != "" {
		identity += " with serial " + entry.USB.Serial
	}
	if usbDevicePresent(entry) {
		return fmt.Sprintf("USB device %s has no serial line. Declare %s in spec.modules", identity, p.LineDriver)
	}
	return fmt.Sprintf("no USB device %s is plugged in", identity)
}

// serioPort returns the name of the serio port that serport registered
// under a tty, the device nodes that the port's driver created, and
// whether a driver is bound to the port.
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

// describeSerio formats one status as a console line.
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
