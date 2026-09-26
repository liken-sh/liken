package main

// What the serio walk reports for each declared entry.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

// lineStatus reports one matched line. It starts the line's holder
// when the line has none and nothing stands in the way, and returns
// that holder for the walk to wait on outside the lock.
func (r *serioRegistry) lineStatus(s machine.SerioStatus, p machine.SerioProtocol, line serialLineInfo) (machine.SerioStatus, *serioHolder) {
	if h, held := r.holders[line.tty]; held {
		return r.settledStatus(s, p, line, h), nil
	}
	failures := 0
	if refusal, refused := r.refusals[line.tty]; refused && refusal.identity == line.identity {
		if r.now().Before(refusal.retryAt) {
			s.State, s.Message = machine.SerioRefused, refusal.message
			return s, nil
		}
		failures = refusal.failures
	}
	// The modules are checked before the open, so a machine that is
	// missing one never touches the line. The load that adds a module
	// sends a uevent, and the walk after it tries again.
	if missing := missingSerioModules(p); len(missing) > 0 {
		s.State = machine.SerioRefused
		s.Message = "declare " + strings.Join(missing, " and ") + " in spec.modules"
		return s, nil
	}
	delete(r.refusals, line.tty)
	h := startSerioHolder(r.open, filepath.Join(devRoot, line.tty), p, r.nudge)
	h.identity, h.started, h.failures = line.identity, r.now(), failures
	r.holders[line.tty] = h
	s.State = machine.SerioRefused
	s.Message = fmt.Sprintf("the attach calls did not return within %s", serioSettleTimeout)
	return s, h
}

// settledStatus reports a line from its holder, without waiting. A
// holder whose calls have not returned reports the wait. A holder whose
// attach failed leaves the registry, and its tty is refused.
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
			r.refuse(line.tty, h, h.attachErr.Error(), 0)
		}
		s.State, s.Message = machine.SerioRefused, h.attachErr.Error()
		return s
	}
	port, nodes, bound := serioPort(line.dir)
	s.Port, s.Nodes, s.Message = port, nodes, ""
	// A port with no driver is a probe that failed, for example when
	// the adapter did not answer the driver's first commands, and it
	// has no CEC device. A port that is not there yet is the moment
	// before the kernel registers it, and the registration's uevents
	// bring the next walk.
	if port != "" && !bound {
		s.State = machine.SerioRefused
		s.Message = fmt.Sprintf("%s on %s has no driver: %s did not bind it; the kernel log names the cause",
			port, line.tty, p.Driver)
		return s
	}
	s.State = machine.SerioAttached
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
