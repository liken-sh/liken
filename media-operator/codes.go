package main

// A controller declares every code it can report in its nodes'
// capability bitmaps, readable the moment a node opens and complete
// with no button pressed. The standing pod publishes that declared set
// retained, this desk folds it in, and the reconcile pass reports the
// gap, the declared controls the compiled table maps to nothing, on the
// Remote's status. The gap is derived on every pass and never
// accumulated, so erasing the status loses nothing.

import (
	"slices"
	"sync"
)

// remoteCodes is the document the standing pod publishes retained at
// every node open: the key codes and the hat axes its nodes declare.
// It carries numbers alone, because the names are the API and the
// numbers are the wire.
type remoteCodes struct {
	Keys []uint16 `json:"keys,omitempty"`
	Axes []uint16 `json:"axes,omitempty"`
}

// codesDesk is the boundary between the codes topic and the reconcile
// loop, shaped like the report desk beside it: the bus thread folds
// messages in, and the pass reads one answer out.
type codesDesk struct {
	mutex    sync.Mutex
	declared map[string]remoteCodes
	online   map[string]bool
	wake     chan<- struct{}
}

func newCodesDesk(wake chan<- struct{}) *codesDesk {
	return &codesDesk{
		declared: map[string]remoteCodes{},
		online:   map[string]bool{},
		wake:     wake,
	}
}

// setCodes folds one controller's document in. A document that differs
// from the one held wakes the loop; a repeat, which every reconnect
// delivers, wakes nothing.
func (c *codesDesk) setCodes(key string, codes remoteCodes) {
	c.mutex.Lock()
	previous, had := c.declared[key]
	c.declared[key] = codes
	c.mutex.Unlock()
	if !had || !sameCodes(previous, codes) {
		poke(c.wake)
	}
}

// clear drops one controller's document. The pod publishes the empty
// retained payload when its controller's nodes vanish, so the desk
// holds nothing for a controller that slept.
func (c *codesDesk) clear(key string) {
	c.mutex.Lock()
	_, had := c.declared[key]
	delete(c.declared, key)
	c.mutex.Unlock()
	if had {
		poke(c.wake)
	}
}

// setAvailability records whether the standing pod that reports one
// controller is up, from the availability topic the pod names as its
// Last Will.
func (c *codesDesk) setAvailability(key string, online bool) {
	c.mutex.Lock()
	previous, had := c.online[key]
	c.online[key] = online
	c.mutex.Unlock()
	if !had || previous != online {
		poke(c.wake)
	}
}

// codesFor returns what one controller declares, and whether the desk
// holds an answer. A controller whose pod is offline declares nothing,
// because a retained document outlives the pod that wrote it.
func (c *codesDesk) codesFor(key string) (remoteCodes, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	codes, held := c.declared[key]
	if !held {
		return remoteCodes{}, false
	}
	if online, heard := c.online[key]; heard && !online {
		return remoteCodes{}, false
	}
	return codes, true
}

// retain shrinks the desk to the Remotes the cluster still holds, so a
// long-running operator keeps no entry for a deleted Remote.
func (c *codesDesk) retain(live map[string]bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	for key := range c.declared {
		if !live[key] {
			delete(c.declared, key)
		}
	}
	for key := range c.online {
		if !live[key] {
			delete(c.online, key)
		}
	}
}

func sameCodes(current, arriving remoteCodes) bool {
	return slices.Equal(current.Keys, arriving.Keys) &&
		slices.Equal(current.Axes, arriving.Axes)
}

// The two words status.unbound uses for an entry's event type.
const (
	unboundKey  = "key"
	unboundAxis = "abs"
)

// unboundCodes is the gap the status reports: every declared control
// the table maps to nothing. A key code passes as itself, so it is
// unbound only where a row drops it with none or the kernel gives it
// no name at all. A hat axis is unbound where no row of it names a
// key, which the base makes rare. The gap is by code, so a row on one
// direction of a hat binds the whole axis. It is derived on every
// pass, so it empties as the table grows.
func unboundCodes(declared remoteCodes, table []compiledBinding) []UnboundCode {
	dropped := map[uint16]bool{}
	named := map[uint16]bool{}
	for _, row := range table {
		switch row.EventType {
		case evKey:
			if row.Key == keyNone {
				dropped[row.Code] = true
			}
		case evAbs:
			if row.Key != keyNone {
				named[row.Code] = true
			}
		}
	}
	var unbound []UnboundCode
	for _, code := range declared.Keys {
		if !dropped[code] && keyCodeNames[code] != "" {
			continue
		}
		unbound = append(unbound, UnboundCode{
			Code: code, Name: keyCodeNames[code], Type: unboundKey})
	}
	for _, code := range declared.Axes {
		if named[code] {
			continue
		}
		unbound = append(unbound, UnboundCode{
			Code: code, Name: axisCodeNames[code], Type: unboundAxis})
	}
	return unbound
}
