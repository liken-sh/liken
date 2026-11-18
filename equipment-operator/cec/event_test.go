package cec_test

// The in-memory bus never sends a lost-messages event or an event that
// NextEvent ignores, so these tests read from oneEvent, a handle whose
// every ioctl returns the same event.

import (
	"testing"
	"time"
	"unsafe"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

type oneEvent struct {
	event cec.KernelEvent
}

func (h oneEvent) Ioctl(request uintptr, argument unsafe.Pointer) error {
	*(*cec.KernelEvent)(argument) = h.event
	return nil
}

func (h oneEvent) Wait(time.Duration) (cec.Readiness, error) { return cec.Readiness{Event: true}, nil }
func (h oneEvent) Close() error                              { return nil }

func TestNextEventReadsEachKind(t *testing.T) {
	lost := cec.KernelEvent{Event: cec.EventLostMsgs}
	lost.Raw[0] = 3
	cases := []struct {
		name  string
		event cec.KernelEvent
		want  cec.Event
	}{
		{"lost messages", lost, cec.Event{Lost: 3}},
		{"CEC_EVENT_PIN_CEC_LOW, which NextEvent ignores", cec.KernelEvent{Event: 3}, cec.Event{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := cec.New(oneEvent{c.event}).NextEvent()

			mustSucceed(t, err)
			if got != c.want {
				t.Errorf("NextEvent() = %+v, want %+v", got, c.want)
			}
		})
	}
}
