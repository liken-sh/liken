package main

// The store's half of the arrangement protocol, liken_arrangement_v1.
//
// The Layout that a Display names owns the screen, and the operator
// gives each claim a region of it. Inside a region, one client of the
// claim can arrange the claim's own surfaces: it binds the protocol
// on its Wayland connection, and the module grants it the claim when
// no other client holds it. The module runs every arrangement itself,
// on each repaint of the output, so no arrangement passes through
// this operator. The module reports four facts, and the operator
// reads them only to show who arranges each claim:
//
//	surface-app-id <surface id> <app_id>
//	arranger <socket> <pid>
//	arranger-gone <socket> <released|disconnected>
//	arrangement <socket> <serial> <name>
//
// An arrangement names surfaces by their xdg app_id, so the app_id of
// each surface is part of what the operator shows. The pid is the
// arranger's process as the compositor reads it from the socket's
// peer credentials, in the compositor's pid namespace. A client in
// another pod is in another pid namespace, and the kernel then
// reports 0. The operator names the arranger by the pods that hold
// its claim instead (arrangers.go).

import (
	"strings"
)

// The two reasons an arranger leaves. Released is a client that
// destroyed its arranger and gave the region up on purpose.
// Disconnected is a client whose connection ended with the arranger
// still held, which is how a crash looks.
const (
	arrangerReleased     = "released"
	arrangerDisconnected = "disconnected"
)

// The verbs of the arrangement protocol's reports.
const (
	layoutSurfaceAppIDEvent = "surface-app-id"
	layoutArrangerEvent     = "arranger"
	layoutArrangerGoneEvent = "arranger-gone"
	layoutArrangementEvent  = "arrangement"
)

// layoutArranger is the client that arranges one claim's region. The
// socket is the claim's, the way a surface's is. Arrangement is the
// name of the arrangement it committed last, and empty before its
// first commit.
type layoutArranger struct {
	Socket      string
	PID         int
	Arrangement string
}

// rememberArrangement puts one report of the arrangement protocol in
// the store. It answers whether it read the line, and whether the
// line was one of these reports at all, so remember reads every other
// verb itself.
//
// The app_id and the name are the rest of the line, because each is a
// string the client chose, and the module copies it as the client
// sent it.
func (l *layoutLink) rememberArrangement(text string) (read, arranging bool) {
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
		return l.rememberAppID(rest), true
	case layoutArrangerEvent:
		fields := strings.Fields(rest)
		if len(fields) != 2 {
			return false, true
		}
		pid, read := layoutNumber(fields[1])
		if !read || pid < 0 {
			return false, true
		}
		// A new grant starts with no arrangement, because the module
		// returns every surface to fill the region when the claim's
		// last arranger leaves.
		l.arrangers[fields[0]] = layoutArranger{Socket: fields[0], PID: pid}
		delete(l.departures, fields[0])
		return true, true
	case layoutArrangerGoneEvent:
		return l.rememberDeparture(rest), true
	}
	return l.rememberCommit(rest), true
}

// rememberAppID records the app_id of one surface the store holds. An
// app_id for a surface the store never saw is dropped, the way a size
// is: the socket is the surface's identity, and an app_id carries none.
// An empty app_id is a client that cleared it.
func (l *layoutLink) rememberAppID(rest string) bool {
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

// rememberDeparture records an arranger that left, and why. A line
// with no reason comes from a module older than the reason, and reads
// as a disconnect, the one way such a module reported a departure
// that the operator could not tell from a crash.
func (l *layoutLink) rememberDeparture(rest string) bool {
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

// rememberCommit records the name of the arrangement an arranger
// committed. The serial is the client's own count, which the module
// sends back to the client when the arrangement lands. The operator
// checks that it is a number and keeps only the name, because the
// status changes when the name does and not on every commit.
func (l *layoutLink) rememberCommit(rest string) bool {
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
