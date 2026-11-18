package main

// A controller's input devices list in their capability bitmaps every
// key code and hat axis they can send, so the operator can report the
// codes that no row maps to a key before anyone presses a button. The
// Remote's pod publishes those codes as a retained message, this file
// caches them, and the reconcile pass compares them with the compiled
// key table to fill status.unbound on the Remote. The pass computes
// status.unbound again each time, so a lost status loses nothing.

import (
	"slices"
	"sync"
)

// remoteCodes is the retained message that the Remote's pod publishes
// each time it opens an input device. The names of the codes are in
// keyCodeNames and axisCodeNames.
type remoteCodes struct {
	Keys []uint16 `json:"keys,omitempty"`
	Axes []uint16 `json:"axes,omitempty"`
}

// codesCache caches what each controller's pod has published on the
// codes and availability topics. The message handler writes it, and the
// reconcile pass reads it. A change wakes the pass through wake.
type codesCache struct {
	mutex    sync.Mutex
	declared map[string]remoteCodes
	online   map[string]bool
	wake     chan<- struct{}
}

func newCodesCache(wake chan<- struct{}) *codesCache {
	return &codesCache{
		declared: map[string]remoteCodes{},
		online:   map[string]bool{},
		wake:     wake,
	}
}

// setCodes stores one controller's codes. It wakes the pass only when
// the codes differ from the stored ones, because the broker redelivers
// the same retained message on every reconnect.
func (c *codesCache) setCodes(key string, codes remoteCodes) {
	c.mutex.Lock()
	previous, had := c.declared[key]
	c.declared[key] = codes
	c.mutex.Unlock()
	if !had || !sameCodes(previous, codes) {
		signalWake(c.wake)
	}
}

// clear deletes one controller's codes. The pod publishes an empty
// retained payload when the controller's input devices disappear.
func (c *codesCache) clear(key string) {
	c.mutex.Lock()
	_, had := c.declared[key]
	delete(c.declared, key)
	c.mutex.Unlock()
	if had {
		signalWake(c.wake)
	}
}

// setAvailability records whether the pod that reports one controller
// is online, from its availability topic. The pod's MQTT Last Will is
// the offline payload, so the broker publishes it when the pod dies.
func (c *codesCache) setAvailability(key string, online bool) {
	c.mutex.Lock()
	previous, had := c.online[key]
	c.online[key] = online
	c.mutex.Unlock()
	if !had || previous != online {
		signalWake(c.wake)
	}
}

// codesFor returns the codes of one controller, and false when there
// are none to use. The broker keeps a retained message after the pod
// that published it dies, so codesFor ignores the codes while the pod's
// availability is offline.
func (c *codesCache) codesFor(key string) (remoteCodes, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	codes, found := c.declared[key]
	if !found {
		return remoteCodes{}, false
	}
	if online, reported := c.online[key]; reported && !online {
		return remoteCodes{}, false
	}
	return codes, true
}

// retain deletes the entries of Remotes that are not in live, so a
// deleted Remote does not stay cached for the life of the operator.
func (c *codesCache) retain(live map[string]bool) {
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

// The values of the type field of an entry in status.unbound.
const (
	unboundKey  = "key"
	unboundAxis = "abs"
)

// unboundCodes returns the declared codes that the table does not map
// to a key, for status.unbound. A key code with no row is sent as
// itself, so it is unbound only when a row maps it to keyNone or when
// keyCodeNames has no name for it. An axis is unbound when no row maps
// its code to a key. A row for one direction binds the whole axis,
// because the check compares codes and not values.
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
