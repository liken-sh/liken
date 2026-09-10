package main

// The store: what the layout module reports about the compositor it
// runs in, and the rectangles the operator states back to it.
//
// The module is the one party that sees a surface arrive, change
// size, and go, so this store is the operator's whole picture of what
// each screen shows. It holds no decision: a placement pass reads a
// snapshot of it, decides, and states the placements through the
// link.

import (
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
)

// The verbs the module sends. A line that starts with anything else
// is a line this operator does not read.
const (
	layoutHelloEvent       = "hello"
	layoutOutputEvent      = "output"
	layoutOutputGoneEvent  = "output-gone"
	layoutSurfaceEvent     = "surface"
	layoutSurfaceSizeEvent = "surface-size"
	layoutSurfaceGoneEvent = "surface-gone"
)

// rect is a rectangle in one output's logical pixels. The module adds
// the output's own position in the global space, so a rectangle is
// stated against the screen it shows on and never against the desktop
// the compositor lays out.
type rect struct {
	X, Y, W, H int
}

// layoutOutput is what the module reports about one output: the
// logical size the compositor lays out in, and the scale it states to
// its clients. On a 4K panel at scale 2 the logical size is 1920 by
// 1080, which is the size every rectangle for that screen is in.
type layoutOutput struct {
	Connector string
	Width     int
	Height    int
	Scale     int
}

// layoutSurface is one surface the compositor holds. The socket is
// the claim: the module reports the listening socket the client
// arrived on, and a prepared claim's socket is wayland-<claim UID>. A
// surface on wayland-0 belongs to no claim.
type layoutSurface struct {
	ID     int
	Socket string
	Width  int
	Height int
}

// layoutState is one reading of the store. Serving says whether the
// module answers this operator, and Reason says why it does not. The
// maps are copies, so a reader holds a picture that no event changes
// under it.
type layoutState struct {
	Serving  bool
	Reason   string
	Outputs  map[string]layoutOutput
	Surfaces map[int]layoutSurface
}

// state reads the store. The placement pass reads it once per pass
// and decides from the copy, so an event that lands mid-pass belongs
// to the next one.
func (l *layoutLink) state() layoutState {
	if l == nil {
		return layoutState{Reason: "the operator wired no layout module"}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return layoutState{
		Serving:  l.serving,
		Reason:   l.reason,
		Outputs:  maps.Clone(l.outputs),
		Surfaces: maps.Clone(l.surfaces),
	}
}

// event puts one event in the store and wakes the operator's loop.
func (l *layoutLink) event(text string) {
	if !l.remember(text) {
		fmt.Fprintf(os.Stderr, "the layout module sent %q, which this operator does not read\n", text)
		return
	}
	l.signal()
}

// remember is the store's one writer, and it answers whether it read
// the event. The caller reports what it could not read, because a
// message this operator drops is a surface it will not place.
//
// A size for a surface this store never saw is dropped with the rest:
// the socket the surface arrived on is its identity, and a size
// carries none.
func (l *layoutLink) remember(text string) bool {
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

// layoutNumber reads one decimal integer off the wire.
func layoutNumber(field string) (int, bool) {
	value, err := strconv.Atoi(field)
	return value, err == nil
}

// layoutNumbers reads a run of them, and answers false unless every
// one of them read.
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
