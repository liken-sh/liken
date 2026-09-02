package main

// These tests cover the discovery mode's output: what one line reports
// about an event, and the Keymap entry a person pastes out of it.

import "testing"

func TestDiscoveryLinesTeachTheKeymapEntry(t *testing.T) {
	cases := []struct {
		name  string
		event inputEvent
		want  []string
	}{
		{
			name:  "a button press, which is the one value a Keymap binds",
			event: inputEvent{Type: evKey, Code: 0x130, Value: 1},
			want: []string{
				`event3 "pad" EV_KEY (1) BTN_SOUTH (304) press (1)`,
				"  - press: BTN_SOUTH   # code 304",
			},
		},
		{
			name:  "the release of the same button",
			event: inputEvent{Type: evKey, Code: 0x130, Value: 0},
			want: []string{
				`event3 "pad" EV_KEY (1) BTN_SOUTH (304) release (0): a Keymap binds the press alone`,
			},
		},
		{
			name:  "the autorepeat of a held button",
			event: inputEvent{Type: evKey, Code: 0x130, Value: 2},
			want: []string{
				`event3 "pad" EV_KEY (1) BTN_SOUTH (304) repeat (2): a Keymap binds the press alone`,
			},
		},
		{
			name:  "a media remote's transport key",
			event: inputEvent{Type: evKey, Code: 0x0a4, Value: 1},
			want: []string{
				`event3 "pad" EV_KEY (1) KEY_PLAYPAUSE (164) press (1)`,
				"  - press: KEY_PLAYPAUSE   # code 164",
			},
		},
		{
			name:  "a code the kernel names nothing",
			event: inputEvent{Type: evKey, Code: 0x2ff, Value: 1},
			want: []string{
				`event3 "pad" EV_KEY (1) 767 press (1): the kernel gives this code no name, so a Keymap cannot bind it`,
			},
		},
		{
			name:  "a hat axis in one of its two directions",
			event: inputEvent{Type: evAbs, Code: 0x11, Value: -1},
			want: []string{
				`event3 "pad" EV_ABS (3) ABS_HAT0Y (17) -1`,
				"  - axis: ABS_HAT0Y   # code 17",
				"    value: -1",
			},
		},
		{
			name:  "the hat returning to the middle",
			event: inputEvent{Type: evAbs, Code: 0x11, Value: 0},
			want: []string{
				`event3 "pad" EV_ABS (3) ABS_HAT0Y (17) 0: a Keymap binds -1 and 1, not the return to the middle`,
			},
		},
		{
			name:  "an axis outside the two hats",
			event: inputEvent{Type: evAbs, Code: 0x00, Value: 128},
			want: []string{
				`event3 "pad" EV_ABS (3) 0 128: a Keymap binds the two hat axes alone`,
			},
		},
		{
			name:  "a synchronization frame",
			event: inputEvent{Type: 0x00, Code: 0x00, Value: 0},
			want:  nil,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			lines := discoveryLines(`event3 "pad"`, each.event)
			if len(lines) < len(each.want) {
				t.Fatalf("lines = %v, want at least %v", lines, each.want)
			}
			for index, want := range each.want {
				mustMatch(t, lines[index], want)
			}
			if len(each.want) == 0 {
				mustMatch(t, len(lines), 0)
			}
		})
	}
}

// The fragment's right side is a key name, so a person pastes the
// entry and writes the kernel name the control should report.
func TestTheFragmentAsksForAKeyName(t *testing.T) {
	lines := discoveryLines(`event3 "pad"`, inputEvent{Type: evKey, Code: 0x130, Value: 1})

	mustMatch(t, lines[len(lines)-1], "    key: "+keymapKeyHint)
}
