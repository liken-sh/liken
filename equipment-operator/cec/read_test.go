package cec_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// collected records the messages and events that Read delivers, and is
// safe to use from several goroutines.
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

func TestReadDeliversMessagesAndEventsUntilItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := room()
		_, device := joined(t, bus)
		ctx, cancel := context.WithCancel(t.Context())
		recorded := &collected{}
		done := make(chan error, 1)
		go func() { done <- cec.Read(ctx, device, recorded.message, recorded.event) }()

		bus.Send(cec.ActiveSource(8, 0x1500))
		synctest.Wait()
		messages, events := recorded.counts()
		if messages == 0 || events == 0 {
			t.Fatalf("Read delivered %d messages and %d events, want at least one of each", messages, events)
		}
		cancel()

		mustSucceed(t, <-done)
	})
}

func TestReadEndsWhenTheAdapterLeaves(t *testing.T) {
	adapter, device := joined(t, room())
	recorded := &collected{}
	adapter.Unplug()

	err := cec.Read(t.Context(), device, recorded.message, recorded.event)

	if !cec.IsGone(err) {
		t.Errorf("Read returned %v, want an error that cec.IsGone accepts (ENODEV)", err)
	}
}
