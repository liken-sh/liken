package cec_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/cec"
)

// collected gathers what Read passes on, safely across goroutines.
type collected struct {
	mutex    sync.Mutex
	messages []cec.Message
	events   []cec.Event
}

func (c *collected) message(message cec.Message) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.messages = append(c.messages, message)
}

func (c *collected) event(event cec.Event) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.events = append(c.events, event)
}

func (c *collected) counts() (int, int) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return len(c.messages), len(c.events)
}

func TestReadPassesOnMessagesAndEventsUntilItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := room()
		_, device := joined(t, bus)
		ctx, cancel := context.WithCancel(t.Context())
		seen := &collected{}
		done := make(chan error, 1)
		go func() { done <- cec.Read(ctx, device, seen.message, seen.event) }()

		bus.Send(cec.ActiveSource(8, 0x1500))
		synctest.Wait()
		messages, events := seen.counts()
		if messages == 0 || events == 0 {
			t.Fatalf("read passed on %d messages and %d events", messages, events)
		}
		cancel()

		mustSucceed(t, <-done)
	})
}

func TestReadEndsWhenTheAdapterLeaves(t *testing.T) {
	adapter, device := joined(t, room())
	seen := &collected{}
	adapter.Unplug()

	err := cec.Read(t.Context(), device, seen.message, seen.event)

	if !cec.IsGone(err) {
		t.Errorf("read answered %v, want ENODEV", err)
	}
}
