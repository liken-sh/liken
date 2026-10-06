package main

import "strings"

// A motion in the fake INDI servers: a change that a driver answers
// with Busy and finishes later, as a mount that parks does.

// move makes a driver answer each change to one property with the
// values sent and the state Busy. The motion ends when a test sets the
// state with setState.
func (w *indiWorld) move(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.moving[device+"."+property] = true
}

// interrupt answers a change to a property that moves, as libindi's
// telescope answers TELESCOPE_PARK during a park (Telescope::ISNewSwitch
// in libs/indibase/inditelescope.cpp): it aborts the motion, turns
// every switch Off, and sets Alert.
func interrupt(p *fakeProperty) {
	for i := range p.Members {
		p.Members[i].Value = "Off"
	}
	p.State = "Alert"
}

// settled answers a copy of a definition with the state and the member
// values of a later update.
func settled(p *fakeProperty, v xmlVector) *fakeProperty {
	out := p.copy()
	if v.State != "" {
		out.State = v.State
	}
	for _, m := range v.Members {
		for i := range out.Members {
			if out.Members[i].Name == m.Name {
				out.Members[i].Value = strings.TrimSpace(m.Value)
			}
		}
	}
	return out
}
