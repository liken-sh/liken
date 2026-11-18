package main

// The store keeps what the layout module in the compositor reports:
// outputs, surfaces, and arrangers. It is the operator's only record of
// what each screen shows. A placement pass reads a copy of it and sends
// its placements back through the link.

import (
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
)

// The first word of each line the module sends. The operator ignores
// a line that starts with any other word.
const (
	layoutHelloEvent       = "hello"
	layoutOutputEvent      = "output"
	layoutOutputGoneEvent  = "output-gone"
	layoutSurfaceEvent     = "surface"
	layoutSurfaceSizeEvent = "surface-size"
	layoutSurfaceGoneEvent = "surface-gone"
)

// rect is a rectangle in one output's logical pixels, relative to that
// output. The module adds the output's position in the compositor's
// global coordinate space.
type rect struct {
	X, Y, W, H int
}

// layoutOutput is one output the module reports: the logical size the
// compositor lays out in, and the scale it tells its clients. A 4K
// panel at scale 2 has a logical size of 1920 by 1080, and every
// rectangle for that output is in those units.
type layoutOutput struct {
	Connector string
	Width     int
	Height    int
	Scale     int
}

// layoutSurface is one surface in the compositor. Socket is the
// Wayland socket the client connected through. A claim's socket is
// wayland-<claim UID>, so the socket identifies the claim. A surface
// on wayland-0 belongs to no claim.
type layoutSurface struct {
	ID     int
	Socket string
	Width  int
	Height int
	// AppID is the xdg app_id the client set on the surface, empty
	// until it sets one. An arrangement refers to surfaces by app_id
	// (arrangerstore.go).
	AppID string
}

// layoutState is a copy of the store at one moment. Serving is false
// when the module is not connected, and Reason says why. The maps are
// copies, so later events do not change what a reader holds.
type layoutState struct {
	Serving  bool
	Reason   string
	Outputs  map[string]layoutOutput
	Surfaces map[int]layoutSurface
	// Arrangers holds the arranger of each claim that has one, keyed
	// by the claim's socket.
	Arrangers map[string]layoutArranger
	// Departures holds why each claim's arranger last left, released
	// or disconnected, keyed by the claim's socket.
	Departures map[string]string
	// Generation counts the connections to the module. A reader that
	// tracks what it already sent must send everything again when the
	// generation changes, because the compositor may have restarted
	// with new surface ids and none of the placements.
	Generation int
}

// state returns a copy of the store. A placement pass reads it once,
// so an event that arrives during the pass is handled by the next pass.
func (l *layoutLink) state() layoutState {
	if l == nil {
		return layoutState{Reason: "the operator wired no layout module"}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return layoutState{
		Serving:    l.serving,
		Reason:     l.reason,
		Outputs:    maps.Clone(l.outputs),
		Surfaces:   maps.Clone(l.surfaces),
		Arrangers:  maps.Clone(l.arrangers),
		Departures: maps.Clone(l.departures),
		Generation: l.generation,
	}
}

// event updates the store from one line of the module and wakes the
// operator's loop.
func (l *layoutLink) event(text string) {
	if !l.apply(text) {
		fmt.Fprintf(os.Stderr, "the layout module sent a line that display-operator does not recognize: %q\n", text)
		return
	}
	l.signal()
}

// apply updates the store from one line and returns false when it
// cannot read the line. The caller logs unreadable lines, because a
// dropped line can mean a surface that is never placed.
//
// apply rejects a surface-size line for an unknown surface. Only the
// surface line carries the socket, and without the socket the store
// cannot tell which claim the surface belongs to.
func (l *layoutLink) apply(text string) bool {
	if read, arranging := l.applyArrangement(text); arranging {
		return read
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	verb, args := fields[0], fields[1:]

	l.mu.Lock()
	defer l.mu.Unlock()
	switch verb {
	case layoutOutputEvent:
		if len(args) != 4 {
			return false
		}
		size, read := layoutNumbers(args[1:])
		if !read {
			return false
		}
		l.outputs[args[0]] = layoutOutput{
			Connector: args[0], Width: size[0], Height: size[1], Scale: size[2],
		}
		return true
	case layoutOutputGoneEvent:
		if len(args) != 1 {
			return false
		}
		delete(l.outputs, args[0])
		return true
	case layoutSurfaceEvent:
		if len(args) != 4 {
			return false
		}
		id, named := layoutNumber(args[0])
		size, read := layoutNumbers(args[2:])
		if !named || !read {
			return false
		}
		l.surfaces[id] = layoutSurface{
			ID: id, Socket: args[1], Width: size[0], Height: size[1],
		}
		return true
	case layoutSurfaceSizeEvent:
		if len(args) != 3 {
			return false
		}
		id, named := layoutNumber(args[0])
		size, read := layoutNumbers(args[1:])
		if !named || !read {
			return false
		}
		surface, known := l.surfaces[id]
		if !known {
			return false
		}
		surface.Width, surface.Height = size[0], size[1]
		l.surfaces[id] = surface
		return true
	case layoutSurfaceGoneEvent:
		if len(args) != 1 {
			return false
		}
		id, named := layoutNumber(args[0])
		if !named {
			return false
		}
		delete(l.surfaces, id)
		return true
	}
	return false
}

func layoutNumber(field string) (int, bool) {
	value, err := strconv.Atoi(field)
	return value, err == nil
}

// layoutNumbers parses each field as an integer, and returns false
// unless all of them parse.
func layoutNumbers(fields []string) ([]int, bool) {
	values := make([]int, len(fields))
	for i, field := range fields {
		value, read := layoutNumber(field)
		if !read {
			return nil, false
		}
		values[i] = value
	}
	return values, true
}
