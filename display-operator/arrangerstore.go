package main

// The store's side of the arrangement protocol, liken_arrangement_v1.
//
// The Layout that a Display refers to gives each claim a region of the
// screen. Inside its region, one client of the claim can arrange the
// claim's own surfaces: it binds the protocol on its Wayland
// connection, and the module grants it the claim when no other client
// has it. The module applies each arrangement itself, so no
// arrangement passes through this operator. The operator reads these
// lines from the module only to show who arranges each claim:
//
//	surface-app-id <surface id> <app_id>
//	arranger <socket> <pid>
//	arranger-gone <socket> <released|disconnected>
//	arrangement <socket> <serial> <name>
//
// An arrangement refers to surfaces by their xdg app_id, so the store
// records each surface's app_id. The compositor reads the pid from the
// socket's peer credentials, in its own pid namespace. A client in
// another pod is in another pid namespace, and the kernel reports 0.
// So the operator identifies the arranger by the pods that use its
// claim instead (arrangers.go).

import (
	"strings"
)

// The reasons an arranger leaves. Released means the client
// destroyed its arranger object and gave up the region. Disconnected
// means the client's connection ended while it still had the
// arranger, which is what a crash looks like.
const (
	arrangerReleased     = "released"
	arrangerDisconnected = "disconnected"
)

// The first word of each report line of the arrangement protocol.
const (
	layoutSurfaceAppIDEvent = "surface-app-id"
	layoutArrangerEvent     = "arranger"
	layoutArrangerGoneEvent = "arranger-gone"
	layoutArrangementEvent  = "arrangement"
)

// layoutArranger is the client that arranges one claim's region.
// Socket is the claim's socket, as for a surface. Arrangement is the
// name of the last arrangement it committed, and is empty before its
// first commit.
type layoutArranger struct {
	Socket      string
	PID         int
	Arrangement string
}

// applyArrangement updates the store from one arrangement line. It
// returns whether it could read the line, and whether the line was an
// arrangement line at all. apply handles every other line.
//
// The client chooses the app_id and the arrangement name. The module
// replaces each space and control character in them with an
// underscore, so each is one word at the end of its line.
func (l *layoutLink) applyArrangement(text string) (read, arranging bool) {
	verb, rest, _ := strings.Cut(text, " ")
	switch verb {
	case layoutSurfaceAppIDEvent, layoutArrangerEvent, layoutArrangerGoneEvent, layoutArrangementEvent:
	default:
		return false, false
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	switch verb {
	case layoutSurfaceAppIDEvent:
		return l.applyAppID(rest), true
	case layoutArrangerEvent:
		fields := strings.Fields(rest)
		if len(fields) != 2 {
			return false, true
		}
		pid, read := layoutNumber(fields[1])
		if !read || pid < 0 {
			return false, true
		}
		// A new arranger starts with no arrangement, because the
		// module makes every surface fill the region again when the
		// claim's previous arranger leaves.
		l.arrangers[fields[0]] = layoutArranger{Socket: fields[0], PID: pid}
		delete(l.departures, fields[0])
		return true, true
	case layoutArrangerGoneEvent:
		return l.applyDeparture(rest), true
	}
	return l.applyCommit(rest), true
}

// applyAppID sets the app_id of a surface. It rejects an app_id for an
// unknown surface, for the same reason apply rejects a size.
func (l *layoutLink) applyAppID(rest string) bool {
	number, appID, _ := strings.Cut(rest, " ")
	id, named := layoutNumber(number)
	if !named {
		return false
	}
	surface, known := l.surfaces[id]
	if !known {
		return false
	}
	surface.AppID = appID
	l.surfaces[id] = surface
	return true
}

// applyDeparture removes an arranger and records why it left. A line
// with no reason counts as a disconnect, because the operator cannot
// tell that departure from a crash.
func (l *layoutLink) applyDeparture(rest string) bool {
	fields := strings.Fields(rest)
	if len(fields) == 1 {
		fields = append(fields, arrangerDisconnected)
	}
	if len(fields) != 2 || (fields[1] != arrangerReleased && fields[1] != arrangerDisconnected) {
		return false
	}
	delete(l.arrangers, fields[0])
	l.departures[fields[0]] = fields[1]
	return true
}

// applyCommit records the name of the arrangement an arranger
// committed. It checks that the serial is a number and stores only the
// name, because status changes when the name changes, not on every
// commit.
func (l *layoutLink) applyCommit(rest string) bool {
	socket, after, split := strings.Cut(rest, " ")
	if !split {
		return false
	}
	serial, name, _ := strings.Cut(after, " ")
	if _, read := layoutNumber(serial); !read {
		return false
	}
	arranger, known := l.arrangers[socket]
	if !known {
		return false
	}
	arranger.Arrangement = name
	l.arrangers[socket] = arranger
	return true
}
