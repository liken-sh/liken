package main

import "testing"

// The reader keeps the events a Keymap can bind off a busy topic. Every
// EV_KEY event goes out, the two hat axes go out, and everything else,
// the analog sticks and the EV_SYN and EV_MSC frames, stays off the
// wire.
func TestPublishableKeepsOnlyBindableEvents(t *testing.T) {
	cases := []struct {
		name  string
		event inputEvent
		want  bool
	}{
		{
			name:  "a button press",
			event: inputEvent{Type: evKey, Code: 0x130, Value: 1},
			want:  true,
		},
		{
			name:  "a button release",
			event: inputEvent{Type: evKey, Code: 0x130, Value: 0},
			want:  true,
		},
		{
			name:  "a hat axis",
			event: inputEvent{Type: evAbs, Code: 0x11, Value: -1},
			want:  true,
		},
		{
			name:  "an analog stick",
			event: inputEvent{Type: evAbs, Code: 0x00, Value: 128},
			want:  false,
		},
		{
			name:  "a sync frame",
			event: inputEvent{Type: 0x00, Code: 0x00, Value: 0},
			want:  false,
		},
		{
			name:  "a misc event",
			event: inputEvent{Type: 0x04, Code: 0x04, Value: 589825},
			want:  false,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, publishable(each.event), each.want)
		})
	}
}
