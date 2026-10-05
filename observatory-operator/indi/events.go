package indi

import (
	"context"
	"sync"
	"time"
)

// EventKind says what changed.
type EventKind int

const (
	// Connected means a new connection opened. The store is empty, and
	// the server's baseline follows as Defined events.
	Connected EventKind = iota + 1
	// Disconnected means the connection ended, and Err says why. The
	// store is empty.
	Disconnected
	// Defined means a device defined a property, or defined it again.
	Defined
	// Updated means a device changed a property's state or values.
	Updated
	// Deleted means a device deleted a property, or every property of
	// the device when Property is empty. indiserver deletes every
	// property of each device of a driver that exits.
	Deleted
	// Message is a message from a device, or from the server when
	// Device is empty.
	Message
	// Invalid means the server sent an element that the client could
	// not apply, and Err says why. The client skips the element and
	// reads on.
	Invalid
)

func (k EventKind) String() string {
	switch k {
	case Connected:
		return "Connected"
	case Disconnected:
		return "Disconnected"
	case Defined:
		return "Defined"
	case Updated:
		return "Updated"
	case Deleted:
		return "Deleted"
	case Message:
		return "Message"
	case Invalid:
		return "Invalid"
	}
	return "EventKind(?)"
}

// Event is one change to the store, or one message. An event names
// what changed, and the store holds the values: by the time a
// subscriber reads an event, the store can hold later values.
type Event struct {
	Kind     EventKind
	Device   string
	Property string
	// Message is the text of a Message event, or the message attribute
	// that a device sent with a definition or an update.
	Message   string
	Timestamp time.Time
	Err       error
	// BLOBs holds the BLOBs of an update, decoded, when a caller asked
	// for them with EnableBLOB. The store keeps no BLOB data, so the
	// data is released when the subscriber drops the event.
	BLOBs []BLOB
}

// A subscriber's queue has no limit. The client's reader must never
// wait for a subscriber: while it waits, it reads nothing from the
// socket, and indiserver closes a client whose queue grows past its
// limit. A limited queue would have to drop events, and a subscriber
// cannot tell what it missed.
type subscriber struct {
	mutex sync.Mutex
	queue []Event
	wake  chan struct{}
}

func (s *subscriber) push(e Event) {
	s.mutex.Lock()
	s.queue = append(s.queue, e)
	s.mutex.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *subscriber) pop() (Event, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if len(s.queue) == 0 {
		return Event{}, false
	}
	e := s.queue[0]
	s.queue[0] = Event{}
	s.queue = s.queue[1:]
	return e, true
}

// Subscribe returns a channel of every event from now until ctx ends,
// in order, across every connection that Run opens. The client closes
// the channel when ctx ends.
//
// A channel fits a caller that runs its own loop, such as an operator
// that writes a device's status after each change, and ctx gives the
// subscription a clear end. The events queue for each subscriber with
// no limit, so a subscriber that stops reading holds memory until its
// ctx ends.
func (c *Client) Subscribe(ctx context.Context) <-chan Event {
	s := &subscriber{wake: make(chan struct{}, 1)}
	c.mutex.Lock()
	c.subscribers[s] = struct{}{}
	c.mutex.Unlock()
	out := make(chan Event)
	go func() {
		defer close(out)
		defer func() {
			c.mutex.Lock()
			delete(c.subscribers, s)
			c.mutex.Unlock()
		}()
		for {
			e, ok := s.pop()
			if !ok {
				select {
				case <-s.wake:
					continue
				case <-ctx.Done():
					return
				}
			}
			select {
			case out <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// publish queues an event for every subscriber and wakes every
// WaitFor. The caller holds c.mutex, so the events reach each
// subscriber in the order the store applied them.
func (c *Client) publish(e Event) {
	for s := range c.subscribers {
		s.push(e)
	}
	close(c.changed)
	c.changed = make(chan struct{})
}
