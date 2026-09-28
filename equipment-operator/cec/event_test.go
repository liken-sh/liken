package cec_test

// The events the bus in memory never sends: the kernel's count of lost
// messages, and an event this package does not read. A handle that
// answers one fixed event is the narrowest fake that reaches them.

import (
	"testing"
	"time"
	"unsafe"

	"github.com/liken-sh/equipment-operator/cec"
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
		{"a pin event this package does not read", cec.KernelEvent{Event: 3}, cec.Event{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := cec.New(oneEvent{c.event}).NextEvent()

			mustSucceed(t, err)
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}
